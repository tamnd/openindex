package eval

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestFaithfulness(t *testing.T) {
	if got := Faithfulness([]bool{true, true, false, true}); !approx(got, 0.75) {
		t.Fatalf("got %g want 0.75", got)
	}
	if got := Faithfulness(nil); got != 1 {
		t.Fatalf("no claims should be vacuously faithful, got %g", got)
	}
}

func TestContextPrecisionRewardsRankingRelevantFirst(t *testing.T) {
	// Relevant at ranks 1 and 2: precision@1=1, precision@2=1, mean 1.
	front := ContextPrecisionAtK([]bool{true, true, false, false}, 4)
	// Relevant at ranks 3 and 4: precision@3=1/3, precision@4=2/4, mean ~0.417.
	back := ContextPrecisionAtK([]bool{false, false, true, true}, 4)
	if !approx(front, 1) {
		t.Fatalf("relevant-first should score 1, got %g", front)
	}
	if !(front > back) {
		t.Fatalf("ranking relevant first should beat burying them: %g vs %g", front, back)
	}
	if !approx(back, (1.0/3.0+2.0/4.0)/2) {
		t.Fatalf("back score wrong: %g", back)
	}
}

func TestContextPrecisionNoRelevant(t *testing.T) {
	if got := ContextPrecisionAtK([]bool{false, false}, 2); got != 0 {
		t.Fatalf("no relevant passage should score 0, got %g", got)
	}
}

func TestContextPrecisionClampsK(t *testing.T) {
	if got := ContextPrecisionAtK([]bool{true}, 100); !approx(got, 1) {
		t.Fatalf("k beyond the list should clamp, got %g", got)
	}
}

func TestContextRecall(t *testing.T) {
	if got := ContextRecall([]bool{true, false}); !approx(got, 0.5) {
		t.Fatalf("got %g want 0.5", got)
	}
	if got := ContextRecall(nil); got != 1 {
		t.Fatalf("no reference claims should score 1, got %g", got)
	}
}

func TestCitationRecallAndPrecision(t *testing.T) {
	if got := CitationRecall([]bool{true, true, true, false}); !approx(got, 0.75) {
		t.Fatalf("recall got %g want 0.75", got)
	}
	if got := CitationPrecision([]bool{true, false}); !approx(got, 0.5) {
		t.Fatalf("precision got %g want 0.5", got)
	}
}

func TestF1(t *testing.T) {
	if got := F1(0.8, 0.6); !approx(got, 2*0.8*0.6/(0.8+0.6)) {
		t.Fatalf("f1 got %g", got)
	}
	if got := F1(0, 0); got != 0 {
		t.Fatalf("f1 of zeros should be 0, got %g", got)
	}
}

func TestMean(t *testing.T) {
	if got := Mean([]float64{0.5, 1.0, 0.0}); !approx(got, 0.5) {
		t.Fatalf("got %g want 0.5", got)
	}
	if got := Mean(nil); got != 0 {
		t.Fatalf("empty mean should be 0, got %g", got)
	}
}

// TestALCEReferenceTargets pins the harness against the published ASQA
// best-system numbers from doc 09.6, so the metric arithmetic stays comparable
// to the literature: about 84.8 percent citation recall and 81.6 percent
// precision. We build judgment slices that realize those rates and confirm the
// functions report them back.
func TestALCEReferenceTargets(t *testing.T) {
	recall := make([]bool, 1000)
	for i := range 848 {
		recall[i] = true
	}
	precision := make([]bool, 1000)
	for i := range 816 {
		precision[i] = true
	}
	if got := CitationRecall(recall); !approx(got, 0.848) {
		t.Fatalf("recall got %g want 0.848", got)
	}
	if got := CitationPrecision(precision); !approx(got, 0.816) {
		t.Fatalf("precision got %g want 0.816", got)
	}
}
