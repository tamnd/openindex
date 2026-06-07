// Package engine composes the answer pipeline end to end (architecture doc
// 09.1): it threads a query through retrieve, rerank, construct, synthesize, and
// verify-then-cite, over the seams the other answer packages define. It lives in
// its own package because synth and verify both import the root answer package
// for the domain types, so the composition that imports all of them cannot.
//
// The pipeline is deliberately a straight line of seams, so the production
// engine is the same control flow with the gRPC clients dropped in: Retriever
// and Reranker stand in for the serving tier (doc 08), synth.Synthesizer for the
// served model, and verify.Verifier for the NLI guardrail. The router (doc 09.5)
// decides whether a query reaches the pipeline at all; Engine.Answer returns
// ErrSearchRoute for a query the router sends to classic search, so the caller
// short-circuits without touching the model.
package engine

import (
	"context"
	"errors"

	"openindex/answer"
	"openindex/answer/ground"
	"openindex/answer/router"
	"openindex/answer/synth"
	"openindex/answer/verify"
)

// ErrSearchRoute is returned by Answer when the router sends the query to the
// classic search path. It is not a failure: it tells the caller to serve web
// results without synthesizing an answer, which is the cheap, correct outcome
// for a navigational query.
var ErrSearchRoute = errors.New("answer: query routed to classic search, no synthesis")

// Retriever pulls candidate passages for a query. The production implementation
// is the hybrid BM25F-plus-dense fan-out through the serving tier (docs 07, 08);
// a test uses an in-process stub. It returns up to n passages, the wide
// candidate set the reranker filters down.
type Retriever interface {
	Retrieve(ctx context.Context, query string, n int) ([]answer.Passage, error)
}

// Reranker filters a candidate set to the top k by a cross-encoder (doc 07.1),
// the precision gate and the single largest quality lever. The production
// implementation calls the rerank model; a test uses a stub. It must be given a
// pool large enough to contain the answer (the candidate-pool rule, doc 07.1),
// which is why Retrieve pulls many more than k.
type Reranker interface {
	Rerank(ctx context.Context, query string, passages []answer.Passage, k int) ([]answer.Passage, error)
}

// Config holds the pipeline's tunable counts. The zero value is filled with the
// doc 09.1 defaults: a wide candidate set, a small reranked context, and the
// 60 percent utilization sweet spot from synth.
type Config struct {
	CandidatePool int     // passages to retrieve before reranking
	ContextSize   int     // passages to keep after reranking
	PerSource     int     // max passages one source contributes (doc 09.3)
	TokenBudget   int     // model context window in tokens
	Utilization   float64 // fill fraction of the budget
	MinConfidence float32 // entailment floor for a citation to survive
	TokenCost     func(answer.Passage) int
}

// Defaults returns a Config with the doc 09.1 / 09.3 defaults. TokenCost
// estimates four bytes per token, a rough but stable stand-in until the real
// tokenizer is wired with the model.
func Defaults() Config {
	return Config{
		CandidatePool: 50,
		ContextSize:   8,
		PerSource:     3,
		TokenBudget:   8192,
		Utilization:   synth.DefaultUtilization,
		MinConfidence: verify.MinConfidence,
		TokenCost:     func(p answer.Passage) int { return len(p.Text)/4 + 1 },
	}
}

// Engine is the assembled answer pipeline. It owns the seams and the config and
// runs them in order. It holds no per-query state, so one Engine serves many
// concurrent queries.
type Engine struct {
	Classifier  router.Classifier
	Retriever   Retriever
	Reranker    Reranker
	Synthesizer synth.Synthesizer
	Verifier    verify.Verifier
	Config      Config
}

// Answer runs the pipeline for one query and returns the grounded, cited answer.
// It routes first and returns ErrSearchRoute for a non-model query, then
// retrieves a wide candidate set, reranks to the context size, consolidates and
// orders the passages for the model, synthesizes the text, and verifies every
// claim before citing it. An answer with an unsupported claim comes back with
// the bad claim dropped and Verified false, never with an unsupported citation
// shown.
func (e Engine) Answer(ctx context.Context, query string) (answer.Answer, error) {
	cfg := e.Config
	if cfg.CandidatePool == 0 {
		cfg = Defaults()
	}

	if e.Classifier != nil {
		if d := e.Classifier.Classify(query); !router.WantsModel(d.Route) {
			return answer.Answer{}, ErrSearchRoute
		}
	}

	candidates, err := e.Retriever.Retrieve(ctx, query, cfg.CandidatePool)
	if err != nil {
		return answer.Answer{}, err
	}
	reranked, err := e.Reranker.Rerank(ctx, query, candidates, cfg.ContextSize)
	if err != nil {
		return answer.Answer{}, err
	}

	// Construct the context: collapse a source that repeats itself, order for
	// the lost-in-the-middle effect, then trim to the utilization budget.
	passages := synth.Consolidate(reranked, cfg.PerSource)
	passages = synth.Order(passages)
	passages = synth.Budget(passages, cfg.TokenCost, cfg.Utilization, cfg.TokenBudget)

	raw, err := e.Synthesizer.Synthesize(ctx, query, passages)
	if err != nil {
		return answer.Answer{}, err
	}

	// Parse the model's inline citation markers off the text, verify each claim
	// against the passages it cited, then re-insert markers for the survivors.
	clean, claims := parseCitations(raw)
	result := verify.Correct(e.Verifier, claims, passages, cfg.MinConfidence)
	text := ground.Insert(clean, result.Supports)

	citations := make([]answer.Citation, 0, len(result.Supports))
	for _, s := range result.Supports {
		citations = append(citations, answer.Citation{
			Start:        s.Segment.Start,
			End:          s.Segment.End,
			ChunkIndices: s.Chunks,
		})
	}
	return answer.Answer{
		Text:      text,
		Passages:  passages,
		Citations: citations,
		Verified:  result.Verified,
	}, nil
}
