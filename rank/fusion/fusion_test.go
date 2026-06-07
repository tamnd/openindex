package fusion

import (
	"math"
	"testing"

	"openindex"
)

func doc(seg openindex.SegmentID, local openindex.DocID) openindex.GlobalDocID {
	return openindex.GlobalDocID{Segment: seg, Local: local}
}

func ranked(docs ...openindex.GlobalDocID) []Candidate {
	cs := make([]Candidate, len(docs))
	for i, d := range docs {
		cs[i] = Candidate{Doc: d, Rank: i}
	}
	return cs
}

func TestRRFRewardsAgreement(t *testing.T) {
	a := doc(0, 1)
	b := doc(0, 2)
	c := doc(0, 3)
	// a is top of list 1 and present in list 2; b only in list 1; c only in list 2.
	lexical := ranked(a, b)
	dense := ranked(c, a)

	got := RRF(DefaultK, lexical, dense)
	if got[0].Doc != a {
		t.Fatalf("document on both lists should rank first, got %v", got[0].Doc)
	}
	// a appears in two lists, so it must outscore any single-list document.
	for _, f := range got[1:] {
		if f.Score >= got[0].Score {
			t.Fatalf("single-list doc %v scored %g >= two-list doc score %g", f.Doc, f.Score, got[0].Score)
		}
	}
}

func TestRRFScoreValue(t *testing.T) {
	a := doc(0, 1)
	// a at rank 0 in two lists: 1/(k+1) twice.
	got := RRF(60, ranked(a), ranked(a))
	want := openindex.Score(2.0 / 61.0)
	if math.Abs(float64(got[0].Score-want)) > 1e-6 {
		t.Fatalf("RRF score = %g, want %g", got[0].Score, want)
	}
}

func TestRRFDefaultK(t *testing.T) {
	a := doc(0, 1)
	got := RRF(0, ranked(a))
	want := openindex.Score(1.0 / float64(DefaultK+1))
	if math.Abs(float64(got[0].Score-want)) > 1e-6 {
		t.Fatalf("k<=0 should use DefaultK: score=%g want %g", got[0].Score, want)
	}
}

func TestRRFDeterministicTieBreak(t *testing.T) {
	// Two docs at the same rank in symmetric lists get equal scores; the tie
	// must break toward the smaller id, every run.
	a := doc(0, 1)
	b := doc(0, 2)
	for range 5 {
		got := RRF(60, ranked(a), ranked(b))
		if got[0].Doc != a || got[1].Doc != b {
			t.Fatalf("tie break not deterministic: %v then %v", got[0].Doc, got[1].Doc)
		}
	}
}

func TestLinearNormalizesScales(t *testing.T) {
	a := doc(0, 1)
	b := doc(0, 2)
	// List 1 scores on a tiny scale, list 2 on a huge one. Min-max normalization
	// must put them on equal footing so the weights mean what they say.
	l1 := []Candidate{{Doc: a, Score: 0.02, Rank: 0}, {Doc: b, Score: 0.01, Rank: 1}}
	l2 := []Candidate{{Doc: b, Score: 900, Rank: 0}, {Doc: a, Score: 100, Rank: 1}}

	got := Linear([]float32{0.5, 0.5}, l1, l2)
	// a: 0.5*1 + 0.5*0 = 0.5; b: 0.5*0 + 0.5*1 = 0.5. Equal, so deterministic
	// tie-break orders a before b.
	if math.Abs(float64(got[0].Score-got[1].Score)) > 1e-6 {
		t.Fatalf("normalized weighting should tie: %g vs %g", got[0].Score, got[1].Score)
	}
	if got[0].Doc != a {
		t.Fatalf("tie should break toward smaller id, got %v", got[0].Doc)
	}
}

func TestLinearIgnoresZeroWeightAndMissingWeights(t *testing.T) {
	a := doc(0, 1)
	b := doc(0, 2)
	l1 := []Candidate{{Doc: a, Score: 1, Rank: 0}}
	l2 := []Candidate{{Doc: b, Score: 1, Rank: 0}}
	// Only the first list is weighted; the second has weight 0 and is dropped.
	got := Linear([]float32{1, 0}, l1, l2)
	if len(got) != 1 || got[0].Doc != a {
		t.Fatalf("zero-weight list should be ignored, got %+v", got)
	}
}

func TestFromResults(t *testing.T) {
	rs := []openindex.Result{
		{Doc: doc(0, 5), Score: 9},
		{Doc: doc(0, 6), Score: 8},
	}
	cs := FromResults(rs)
	if len(cs) != 2 || cs[0].Rank != 0 || cs[1].Rank != 1 {
		t.Fatalf("ranks should follow position: %+v", cs)
	}
	if cs[0].Doc != rs[0].Doc || cs[0].Score != rs[0].Score {
		t.Fatalf("FromResults dropped fields: %+v", cs[0])
	}
}

func TestEmptyInput(t *testing.T) {
	if got := RRF(60); len(got) != 0 {
		t.Fatalf("no lists should fuse to empty, got %v", got)
	}
	if got := RRF(60, nil, nil); len(got) != 0 {
		t.Fatalf("empty lists should fuse to empty, got %v", got)
	}
}
