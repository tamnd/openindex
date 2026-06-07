// Package harness is the CI evaluation gate (implementation doc 12.3 and 12.4).
// The relevance bar and the answer-engine grounding contract are enforced
// mechanically rather than by reviewer vigilance: the harness runs the standard
// metrics over a versioned, judged query set and fails the build when a metric
// regresses past a fixed tolerance, so a ranking or answer change cannot ship
// while quietly lowering quality.
//
// The metric arithmetic lives in rank/eval (NDCG, MRR, Recall) and answer/eval
// (RAGAS faithfulness and context recall, ALCE citation recall and precision).
// This package does not reimplement it; it drives those metrics over a query
// set, aggregates per-query scores into one corpus figure, and compares the
// figure to a baseline. Keeping the gate separate from the metrics means the
// metrics stay pure testable functions and the baseline is the only thing that
// is versioned and argued over.
package harness

import (
	"fmt"
	"slices"
	"time"

	aeval "openindex/answer/eval"
	"openindex/rank/eval"
)

// Query is one judged relevance query: an id and its graded judgments (qrels),
// document id to relevance grade, exactly the shape rank/eval consumes. A
// document absent from Rel is grade 0 (not relevant).
type Query struct {
	ID  string
	Rel map[string]int
}

// Ranker is the system under test: it returns the ranked document ids for a
// query. In CI it is the real retrieval path; in a unit test it is a fixed list.
type Ranker func(Query) []string

// RelevanceReport is the corpus-level relevance result: the mean of each metric
// over the query set, at cutoff K. These are the trec_eval-comparable figures of
// doc 12.3 (NDCG@10 primary, MRR@10 shallow, Recall@K first-stage ceiling).
type RelevanceReport struct {
	K       int
	NDCG    float64
	MRR     float64
	Recall  float64
	Queries int
}

// EvalRelevance runs the ranker over every query and returns the mean NDCG@K,
// MRR@K, and Recall@K. An empty query set returns a zero report rather than
// dividing by zero, so a misconfigured harness reads as a hard regression rather
// than a passing build.
func EvalRelevance(queries []Query, rank Ranker, k int) RelevanceReport {
	ndcg := make([]float64, 0, len(queries))
	mrr := make([]float64, 0, len(queries))
	recall := make([]float64, 0, len(queries))
	for _, q := range queries {
		ranked := rank(q)
		ndcg = append(ndcg, eval.NDCGAtK(ranked, q.Rel, k))
		mrr = append(mrr, eval.MRRAtK(ranked, q.Rel, k))
		recall = append(recall, eval.RecallAtK(ranked, q.Rel, k))
	}
	return RelevanceReport{
		K:       k,
		NDCG:    eval.Mean(ndcg),
		MRR:     eval.Mean(mrr),
		Recall:  eval.Mean(recall),
		Queries: len(queries),
	}
}

// Baseline is the versioned relevance floor a change must hold. A metric is a
// regression when it drops more than Tolerance below its baseline; an
// improvement never fails. Tolerance absorbs run-to-run noise so the gate flags
// real drops, not jitter, and is checked into the repository next to the qrels.
type Baseline struct {
	NDCG      float64
	MRR       float64
	Recall    float64
	Tolerance float64
}

// Regression names a metric that fell past tolerance, with the baseline it was
// held to and the value it reached, so the failing build says exactly what
// dropped and by how much.
type Regression struct {
	Metric   string
	Baseline float64
	Got      float64
}

// String renders a regression for a test failure message.
func (r Regression) String() string {
	return fmt.Sprintf("%s regressed: baseline %.4f, got %.4f (down %.4f)", r.Metric, r.Baseline, r.Got, r.Baseline-r.Got)
}

// GateRelevance compares a report to a baseline and returns the regressions. An
// empty result means the build passes. A test calls this and fails when the
// result is non-empty, which is the CI gate of doc 12.3.
func GateRelevance(b Baseline, r RelevanceReport) []Regression {
	var regs []Regression
	check := func(name string, base, got float64) {
		if got < base-b.Tolerance {
			regs = append(regs, Regression{Metric: name, Baseline: base, Got: got})
		}
	}
	check(fmt.Sprintf("NDCG@%d", r.K), b.NDCG, r.NDCG)
	check(fmt.Sprintf("MRR@%d", r.K), b.MRR, r.MRR)
	check(fmt.Sprintf("Recall@%d", r.K), b.Recall, r.Recall)
	return regs
}

// AnswerJudgment is the per-query verdict set for one answer, the boolean
// judgments answer/eval consumes: which atomic claims the context entails
// (faithfulness), which answer sentences their citations entail (ALCE citation
// recall), which individual citations are necessary (ALCE citation precision),
// and which reference claims were supported by what retrieval fetched (RAGAS
// context recall). A caller produces these claim-by-claim with the same NLI
// model the online guardrail uses, so the arithmetic stays a pure function.
type AnswerJudgment struct {
	ClaimsEntailed     []bool
	SentencesEntailed  []bool
	CitationsNecessary []bool
	ReferenceSupported []bool
}

// AnswerReport is the corpus-level answer-engine result, the metrics of doc
// 12.4: RAGAS faithfulness and context recall, and the ALCE citation pair with
// their F1, each meaned over the query set.
type AnswerReport struct {
	Faithfulness      float64
	CitationRecall    float64
	CitationPrecision float64
	CitationF1        float64
	ContextRecall     float64
	Queries           int
}

// EvalAnswer computes the answer-engine report over a set of per-query
// judgments. The citation F1 is the harmonic mean of the corpus recall and
// precision, the single summary doc 09.6 reports against its per-dataset targets.
func EvalAnswer(judgments []AnswerJudgment) AnswerReport {
	faith := make([]float64, 0, len(judgments))
	crecall := make([]float64, 0, len(judgments))
	cprec := make([]float64, 0, len(judgments))
	crec := make([]float64, 0, len(judgments))
	for _, j := range judgments {
		faith = append(faith, aeval.Faithfulness(j.ClaimsEntailed))
		crecall = append(crecall, aeval.CitationRecall(j.SentencesEntailed))
		cprec = append(cprec, aeval.CitationPrecision(j.CitationsNecessary))
		crec = append(crec, aeval.ContextRecall(j.ReferenceSupported))
	}
	rep := AnswerReport{
		Faithfulness:      aeval.Mean(faith),
		CitationRecall:    aeval.Mean(crecall),
		CitationPrecision: aeval.Mean(cprec),
		ContextRecall:     aeval.Mean(crec),
		Queries:           len(judgments),
	}
	rep.CitationF1 = aeval.F1(rep.CitationRecall, rep.CitationPrecision)
	return rep
}

// AnswerBaseline is the versioned answer-engine floor. The realistic per-dataset
// targets of doc 09.6 (ASQA near 84.8/81.6 citation recall and precision, ELI5
// near 69.3/67.8) are the kind of value that goes here, rather than an inflated
// single headline.
type AnswerBaseline struct {
	Faithfulness      float64
	CitationRecall    float64
	CitationPrecision float64
	ContextRecall     float64
	Tolerance         float64
}

// GateAnswer compares an answer report to its baseline and returns the
// regressions, the grounding gate of doc 12.4 that sits on top of the relevance
// gate. An empty result passes.
func GateAnswer(b AnswerBaseline, r AnswerReport) []Regression {
	var regs []Regression
	check := func(name string, base, got float64) {
		if got < base-b.Tolerance {
			regs = append(regs, Regression{Metric: name, Baseline: base, Got: got})
		}
	}
	check("Faithfulness", b.Faithfulness, r.Faithfulness)
	check("CitationRecall", b.CitationRecall, r.CitationRecall)
	check("CitationPrecision", b.CitationPrecision, r.CitationPrecision)
	check("ContextRecall", b.ContextRecall, r.ContextRecall)
	return regs
}

// Percentile returns the q-quantile (0..1) of a set of latencies by the
// nearest-rank method, the figure a load test asserts against the P99/P99.99
// budget (doc 12.5). It sorts a copy so the caller's slice is untouched, and an
// empty input is 0. The serving contract is a tail contract, so the load test
// exercises this, not the mean.
func Percentile(durations []time.Duration, q float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	slices.Sort(sorted)
	switch {
	case q <= 0:
		return sorted[0]
	case q >= 1:
		return sorted[len(sorted)-1]
	}
	rank := int(q*float64(len(sorted)-1) + 0.5)
	return sorted[rank]
}
