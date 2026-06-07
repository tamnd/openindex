package synth

import (
	"testing"

	"openindex"
	"openindex/answer"
)

func psg(source string, score openindex.Score) answer.Passage {
	return answer.Passage{Source: source, Score: score}
}

func scored(score openindex.Score) answer.Passage { return answer.Passage{Score: score} }

func TestOrderPutsStrongestAtEdges(t *testing.T) {
	// Scores 5,4,3,2,1 should be dealt outward-in: 5 first, 4 last, 3 second,
	// 2 second-to-last, 1 in the middle.
	in := []answer.Passage{scored(3), scored(1), scored(5), scored(2), scored(4)}
	got := Order(in)
	want := []openindex.Score{5, 3, 1, 2, 4}
	for i, w := range want {
		if got[i].Score != w {
			t.Fatalf("position %d: got %g want %g (full %v)", i, got[i].Score, w, scores(got))
		}
	}
}

func TestOrderDoesNotMutateInput(t *testing.T) {
	in := []answer.Passage{scored(1), scored(3), scored(2)}
	_ = Order(in)
	if in[0].Score != 1 || in[1].Score != 3 || in[2].Score != 2 {
		t.Fatalf("Order mutated its input: %v", scores(in))
	}
}

func TestOrderSmallInputs(t *testing.T) {
	if got := Order(nil); len(got) != 0 {
		t.Fatalf("nil should order to empty, got %v", got)
	}
	one := []answer.Passage{scored(7)}
	if got := Order(one); len(got) != 1 || got[0].Score != 7 {
		t.Fatalf("single passage should pass through, got %v", scores(got))
	}
}

func TestBudgetFillsToUtilization(t *testing.T) {
	// Budget 100 tokens, util 0.6, so the ceiling is 60. Passages cost 30 each;
	// two fit (60), the third would exceed.
	ps := []answer.Passage{scored(9), scored(8), scored(7), scored(6)}
	cost := func(answer.Passage) int { return 30 }
	got := Budget(ps, cost, 0.6, 100)
	if len(got) != 2 {
		t.Fatalf("two of four should fit a 60-token ceiling, got %d", len(got))
	}
}

func TestBudgetDefaultUtilization(t *testing.T) {
	ps := []answer.Passage{scored(1), scored(1), scored(1)}
	cost := func(answer.Passage) int { return 25 }
	// Default 0.6 of 100 is 60: two fit (50), the third (75) does not.
	if got := Budget(ps, cost, 0, 100); len(got) != 2 {
		t.Fatalf("default utilization should admit two, got %d", len(got))
	}
}

func TestConsolidateCapsPerSource(t *testing.T) {
	ps := []answer.Passage{
		psg("a.test", 9), psg("a.test", 8), psg("a.test", 3),
		psg("b.test", 7),
	}
	got := Consolidate(ps, 2)
	// a.test keeps its top two (9, 8) and drops the weakest (3); b.test keeps
	// its one. Three survive.
	if len(got) != 3 {
		t.Fatalf("expected 3 survivors, got %d (%v)", len(got), scores(got))
	}
	for _, p := range got {
		if p.Source == "a.test" && p.Score == 3 {
			t.Fatal("the weakest a.test passage should have been dropped")
		}
	}
}

func TestConsolidateKeepsUngrouped(t *testing.T) {
	ps := []answer.Passage{psg("", 1), psg("a.test", 9), psg("a.test", 8), psg("", 2)}
	got := Consolidate(ps, 1)
	// Both empty-source passages survive, a.test keeps only its top one.
	if len(got) != 3 {
		t.Fatalf("ungrouped passages should always survive, got %d", len(got))
	}
}

func TestConsolidatePreservesOrder(t *testing.T) {
	ps := []answer.Passage{psg("a", 5), psg("b", 9), psg("a", 4), psg("c", 1)}
	got := Consolidate(ps, 2)
	// No source exceeds the cap, so nothing is dropped and the input order holds.
	want := []openindex.Score{5, 9, 4, 1}
	for i, w := range want {
		if got[i].Score != w {
			t.Fatalf("order not preserved at %d: got %g want %g", i, got[i].Score, w)
		}
	}
}

func TestFreshenBoostsRecent(t *testing.T) {
	now := int64(1_000_000)
	halfLife := int64(100_000)
	// Two passages, equal retrieval score; one is fresh, one is a half-life old.
	ps := []answer.Passage{
		{Score: 1, Published: now},            // recency 1.0
		{Score: 1, Published: now - halfLife}, // recency 0.5
	}
	got := Freshen(ps, now, halfLife, 0.5, 1.0)
	// fresh:  1*0.5 + 1.0*0.5*1 = 1.0 ; old: 1*0.5 + 0.5*0.5*1 = 0.75
	if !(got[0].Score > got[1].Score) {
		t.Fatalf("the fresher passage should score higher: %g vs %g", got[0].Score, got[1].Score)
	}
}

func TestFreshenLeavesUnstampedAlone(t *testing.T) {
	got := Freshen([]answer.Passage{{Score: 4, Published: 0}}, 1000, 100, 0.9, 1.0)
	if got[0].Score != 4 {
		t.Fatalf("a passage with no timestamp should keep its score, got %g", got[0].Score)
	}
}

func TestFreshenZeroWeightIsIdentity(t *testing.T) {
	ps := []answer.Passage{{Score: 3, Published: 500}}
	got := Freshen(ps, 1000, 100, 0, 1.0)
	if got[0].Score != 3 {
		t.Fatalf("zero weight should not change the score, got %g", got[0].Score)
	}
}

func scores(ps []answer.Passage) []openindex.Score {
	out := make([]openindex.Score, len(ps))
	for i, p := range ps {
		out[i] = p.Score
	}
	return out
}
