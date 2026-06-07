package eval

import (
	"math"
	"testing"
)

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.12f, want %.12f", name, got, want)
	}
}

func TestNDCGPerfectRankingIsOne(t *testing.T) {
	rel := map[string]int{"a": 3, "b": 2, "c": 1}
	ranked := []string{"a", "b", "c"}
	approx(t, "NDCG@3 perfect", NDCGAtK(ranked, rel, 3), 1.0)
}

func TestNDCGOrderingMatters(t *testing.T) {
	rel := map[string]int{"a": 3, "b": 0, "c": 0}
	good := NDCGAtK([]string{"a", "b", "c"}, rel, 3)
	bad := NDCGAtK([]string{"b", "c", "a"}, rel, 3)
	if good <= bad {
		t.Fatalf("relevant-first should score higher: %g vs %g", good, bad)
	}
	approx(t, "NDCG@3 relevant first", good, 1.0)
}

func TestDCGKnownValue(t *testing.T) {
	// Grades 3, 2 at positions 1, 2:
	// (2^3-1)/log2(2) + (2^2-1)/log2(3) = 7/1 + 3/1.5849625... = 8.892789...
	rel := map[string]int{"a": 3, "b": 2}
	want := 7.0 + 3.0/math.Log2(3)
	approx(t, "DCG@2", DCGAtK([]string{"a", "b"}, rel, 2), want)
}

func TestNDCGNoRelevant(t *testing.T) {
	rel := map[string]int{}
	if got := NDCGAtK([]string{"a", "b"}, rel, 2); got != 0 {
		t.Fatalf("no judgments should give NDCG 0, got %g", got)
	}
}

func TestMRR(t *testing.T) {
	rel := map[string]int{"x": 1}
	// First relevant at position 3 -> 1/3.
	approx(t, "MRR@10", MRRAtK([]string{"a", "b", "x", "c"}, rel, 10), 1.0/3.0)
	// Relevant exists but outside k -> 0.
	approx(t, "MRR@2 out of window", MRRAtK([]string{"a", "b", "x"}, rel, 2), 0)
	// None relevant -> 0.
	approx(t, "MRR none", MRRAtK([]string{"a", "b"}, map[string]int{}, 2), 0)
}

func TestRecall(t *testing.T) {
	rel := map[string]int{"a": 1, "b": 1, "c": 1, "d": 1}
	// Two of four relevant in the top 3.
	approx(t, "Recall@3", RecallAtK([]string{"a", "x", "b"}, rel, 3), 0.5)
	// All four in a long enough window.
	approx(t, "Recall@10", RecallAtK([]string{"a", "b", "c", "d"}, rel, 10), 1.0)
}

func TestAveragePrecision(t *testing.T) {
	// Relevant at ranks 1 and 3 of 2 total relevant:
	// precision@1 = 1/1, precision@3 = 2/3, AP = (1 + 2/3)/2 = 0.8333...
	rel := map[string]int{"a": 1, "c": 1}
	want := (1.0 + 2.0/3.0) / 2.0
	approx(t, "AP", AveragePrecision([]string{"a", "b", "c"}, rel), want)
}

func TestAveragePrecisionPerfect(t *testing.T) {
	rel := map[string]int{"a": 1, "b": 1}
	approx(t, "AP perfect", AveragePrecision([]string{"a", "b", "c"}, rel), 1.0)
}

func TestMeanIsMAP(t *testing.T) {
	relA := map[string]int{"a": 1}
	relB := map[string]int{"b": 1}
	ap := []float64{
		AveragePrecision([]string{"a"}, relA),      // 1.0
		AveragePrecision([]string{"x", "b"}, relB), // 0.5
	}
	approx(t, "MAP", Mean(ap), 0.75)
	approx(t, "Mean empty", Mean(nil), 0)
}

func TestKLargerThanList(t *testing.T) {
	rel := map[string]int{"a": 2}
	// k beyond the list length must not panic and must score the whole list.
	approx(t, "NDCG@100 perfect short list", NDCGAtK([]string{"a"}, rel, 100), 1.0)
}

func TestGenericOverIntIDs(t *testing.T) {
	// The metrics are generic; integer doc ids work as well as strings.
	rel := map[int]int{7: 3, 9: 1}
	approx(t, "NDCG int ids", NDCGAtK([]int{7, 9}, rel, 2), 1.0)
}
