// Package rank wires the retrieval cascade (architecture doc 07.1): three
// stages of decreasing candidate count and increasing cost, so the latency
// budget is spent cheap-first and expensive-last. The first stage (recall) runs
// on the leaf and produces hundreds to low thousands of candidates by fusing
// lexical (BM25F, package bm25f) and dense retrieval with RRF (package fusion).
// The second stage (relevance) runs the LTR model (package ltr) over the
// assembled feature vector (package feature) and prunes to the top tens to
// hundreds. The third stage (precision) is a cross-encoder on the final tens.
//
// The cardinal constraint is that no reranker recovers a document the first
// stage missed, so first-stage recall is the metric the whole stack protects.
// This package holds the second-stage wiring and the candidate-pool rule; the
// first stage lives in the index and vector packages, and the cross-encoder
// third stage is a later reranker behind the same shape.
package rank

import (
	"sort"

	"openindex"
	"openindex/rank/ltr"
)

// DefaultMinRerankPool is the candidate-pool floor for invoking the model. The
// rule from doc 07.1: an expensive reranker is wasted on a pool of 20 when the
// relevant documents are not in it, so reranking only earns its cost once the
// first stage has produced enough candidates. The spec range is 50 to 100.
const DefaultMinRerankPool = 50

// Candidate is one document leaving the first stage, with the feature vector
// the second stage scores. URL travels through so the reranked output is a
// complete Result without a second lookup.
type Candidate struct {
	Doc      openindex.GlobalDocID
	URL      string
	Features []float32
}

// Reranker is the second stage: it scores candidates with an LTR model and
// returns the top k, subject to the candidate-pool rule.
type Reranker struct {
	model ltr.Model
	// MinPool is the candidate count below which reranking is skipped. Zero
	// selects DefaultMinRerankPool.
	minPool int
}

// NewReranker returns a second-stage reranker for the model. It does not verify
// the model against the feature schema; call ltr.Check at load time for that.
func NewReranker(model ltr.Model, minPool int) *Reranker {
	if minPool <= 0 {
		minPool = DefaultMinRerankPool
	}
	return &Reranker{model: model, minPool: minPool}
}

// Rerank scores the candidates and returns the top k as results, highest score
// first. When the pool is smaller than the minimum, it does not run the model:
// it returns the candidates in their first-stage order (already the best signal
// available) truncated to k, and reports reranked=false. Ties in model score
// break deterministically by segment then local id.
func (r *Reranker) Rerank(cands []Candidate, k int) (results []openindex.Result, reranked bool) {
	if k <= 0 || len(cands) == 0 {
		return nil, false
	}
	if len(cands) < r.minPool {
		return passthrough(cands, k), false
	}

	scored := make([]openindex.Result, len(cands))
	for i := range cands {
		scored[i] = openindex.Result{
			Doc:   cands[i].Doc,
			URL:   cands[i].URL,
			Score: openindex.Score(r.model.Score(cands[i].Features)),
		}
	}
	sort.Slice(scored, func(i, j int) bool { return lessResult(scored[i], scored[j]) })
	return truncate(scored, k), true
}

// passthrough returns the first-stage candidates as results, preserving order.
func passthrough(cands []Candidate, k int) []openindex.Result {
	out := make([]openindex.Result, 0, min(k, len(cands)))
	for i := 0; i < len(cands) && i < k; i++ {
		out = append(out, openindex.Result{Doc: cands[i].Doc, URL: cands[i].URL})
	}
	return out
}

func truncate(rs []openindex.Result, k int) []openindex.Result {
	if k < len(rs) {
		return rs[:k]
	}
	return rs
}

// lessResult is the result comparator: higher score first, ties broken toward
// the smaller document id so the order is deterministic.
func lessResult(a, b openindex.Result) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Doc.Segment != b.Doc.Segment {
		return a.Doc.Segment < b.Doc.Segment
	}
	return a.Doc.Local < b.Doc.Local
}
