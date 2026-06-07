package harness

import (
	"testing"
	"time"
)

// twoQueries is a tiny judged set: each query has one clearly relevant document.
func twoQueries() []Query {
	return []Query{
		{ID: "q1", Rel: map[string]int{"d1": 3, "d2": 1}},
		{ID: "q2", Rel: map[string]int{"d9": 2}},
	}
}

func TestEvalRelevancePerfectRanking(t *testing.T) {
	// A ranker that returns the judged documents in ideal grade order scores a
	// perfect NDCG and MRR, and full recall.
	rank := func(q Query) []string {
		switch q.ID {
		case "q1":
			return []string{"d1", "d2", "d3"}
		default:
			return []string{"d9", "d8"}
		}
	}
	r := EvalRelevance(twoQueries(), rank, 10)
	if r.NDCG < 0.999 {
		t.Fatalf("NDCG = %v, want ~1 for an ideal ranking", r.NDCG)
	}
	if r.MRR != 1 {
		t.Fatalf("MRR = %v, want 1 (relevant doc first every query)", r.MRR)
	}
	if r.Recall != 1 {
		t.Fatalf("Recall = %v, want 1 (all relevant docs returned)", r.Recall)
	}
	if r.Queries != 2 {
		t.Fatalf("Queries = %d, want 2", r.Queries)
	}
}

func TestGateRelevanceCatchesRegression(t *testing.T) {
	// A ranker that buries the relevant document drops NDCG and MRR below the
	// baseline, and the gate must report the regression.
	rank := func(q Query) []string {
		switch q.ID {
		case "q1":
			return []string{"x", "y", "z", "d2", "d1"}
		default:
			return []string{"a", "b", "c", "d9"}
		}
	}
	r := EvalRelevance(twoQueries(), rank, 10)
	base := Baseline{NDCG: 0.9, MRR: 0.9, Recall: 0.9, Tolerance: 0.02}
	regs := GateRelevance(base, r)
	if len(regs) == 0 {
		t.Fatal("a buried-result ranker must trip the relevance gate")
	}
	// The message names the metric and the gap.
	for _, reg := range regs {
		if reg.Got >= reg.Baseline {
			t.Fatalf("a reported regression must have Got below Baseline: %s", reg)
		}
	}
}

func TestGateRelevanceTolerance(t *testing.T) {
	// A report a hair below baseline, but inside tolerance, must pass; the same
	// drop past tolerance must fail. This is the noise-vs-real-drop line.
	r := RelevanceReport{K: 10, NDCG: 0.89, MRR: 0.89, Recall: 0.89}
	pass := GateRelevance(Baseline{NDCG: 0.9, MRR: 0.9, Recall: 0.9, Tolerance: 0.05}, r)
	if len(pass) != 0 {
		t.Fatalf("a drop within tolerance must pass, got %v", pass)
	}
	fail := GateRelevance(Baseline{NDCG: 0.9, MRR: 0.9, Recall: 0.9, Tolerance: 0.005}, r)
	if len(fail) != 3 {
		t.Fatalf("a drop past tolerance must flag all three metrics, got %d", len(fail))
	}
}

func TestEmptyQuerySetReadsAsRegression(t *testing.T) {
	r := EvalRelevance(nil, func(Query) []string { return nil }, 10)
	regs := GateRelevance(Baseline{NDCG: 0.5, MRR: 0.5, Recall: 0.5, Tolerance: 0.01}, r)
	if len(regs) == 0 {
		t.Fatal("an empty query set must score zero and fail a non-zero baseline")
	}
}

func TestEvalAnswerAndGate(t *testing.T) {
	// One answer, fully grounded and tightly cited: the metrics are all 1.
	judgments := []AnswerJudgment{
		{
			ClaimsEntailed:     []bool{true, true},
			SentencesEntailed:  []bool{true},
			CitationsNecessary: []bool{true, true},
			ReferenceSupported: []bool{true},
		},
		{
			ClaimsEntailed:     []bool{true, false}, // one unsupported claim
			SentencesEntailed:  []bool{true, false},
			CitationsNecessary: []bool{true},
			ReferenceSupported: []bool{true, true},
		},
	}
	r := EvalAnswer(judgments)
	if r.Faithfulness <= 0 || r.Faithfulness >= 1 {
		t.Fatalf("faithfulness = %v, want between 0 and 1 with one unsupported claim", r.Faithfulness)
	}
	if r.CitationF1 == 0 {
		t.Fatal("citation F1 should be non-zero when recall and precision are positive")
	}

	// A grounding floor the second answer's faithfulness falls below.
	base := AnswerBaseline{Faithfulness: 0.95, CitationRecall: 0.5, CitationPrecision: 0.5, ContextRecall: 0.5, Tolerance: 0.02}
	regs := GateAnswer(base, r)
	if len(regs) == 0 {
		t.Fatal("a faithfulness drop must trip the answer gate")
	}
}

func TestPercentile(t *testing.T) {
	durs := make([]time.Duration, 0, 100)
	for i := 1; i <= 100; i++ {
		durs = append(durs, time.Duration(i)*time.Millisecond)
	}
	if p99 := Percentile(durs, 0.99); p99 < 99*time.Millisecond {
		t.Fatalf("P99 = %v, want near 99ms", p99)
	}
	if p50 := Percentile(durs, 0.50); p50 < 49*time.Millisecond || p50 > 51*time.Millisecond {
		t.Fatalf("P50 = %v, want near 50ms", p50)
	}
	if got := Percentile(nil, 0.99); got != 0 {
		t.Fatalf("percentile of empty input = %v, want 0", got)
	}
	// The input slice must be left untouched (a copy is sorted).
	unsorted := []time.Duration{3, 1, 2}
	_ = Percentile(unsorted, 0.5)
	if unsorted[0] != 3 {
		t.Fatal("Percentile must not sort the caller's slice in place")
	}
}
