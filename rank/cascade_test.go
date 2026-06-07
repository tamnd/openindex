package rank

import (
	"testing"

	"openindex"
	"openindex/rank/feature"
	"openindex/rank/ltr"
)

// scoreFirstFeature is a model whose score is the first feature, so the test
// controls the ranking directly.
func scoreFirstFeature() ltr.Model {
	w := make([]float32, feature.NumFeatures)
	w[0] = 1
	return ltr.NewLinearModel(w, 0)
}

func cand(local openindex.DocID, first float32) Candidate {
	f := make([]float32, feature.NumFeatures)
	f[0] = first
	return Candidate{
		Doc:      openindex.GlobalDocID{Segment: 0, Local: local},
		URL:      "https://example.test/" + string(rune('a'+int(local))),
		Features: f,
	}
}

func TestRerankSortsByModelScore(t *testing.T) {
	r := NewReranker(scoreFirstFeature(), 3)
	// Input order is not score order; the reranker must fix that.
	cands := []Candidate{cand(0, 0.1), cand(1, 0.9), cand(2, 0.5)}
	got, reranked := r.Rerank(cands, 3)
	if !reranked {
		t.Fatal("a pool at the minimum should be reranked")
	}
	wantOrder := []openindex.DocID{1, 2, 0}
	for i, w := range wantOrder {
		if got[i].Doc.Local != w {
			t.Fatalf("rank %d: got doc %d, want %d", i, got[i].Doc.Local, w)
		}
	}
	if got[0].URL == "" {
		t.Fatal("reranked result should carry the URL through")
	}
}

func TestRerankSkipsSmallPool(t *testing.T) {
	r := NewReranker(scoreFirstFeature(), 50)
	// Below the pool floor: pass through in first-stage order, do not score.
	cands := []Candidate{cand(2, 0.1), cand(0, 0.9), cand(1, 0.5)}
	got, reranked := r.Rerank(cands, 3)
	if reranked {
		t.Fatal("a pool below the minimum must not be reranked")
	}
	for i := range cands {
		if got[i].Doc != cands[i].Doc {
			t.Fatalf("passthrough should keep first-stage order at %d: got %v", i, got[i].Doc)
		}
		if got[i].Score != 0 {
			t.Fatalf("passthrough should not score, got %g at %d", got[i].Score, i)
		}
	}
}

func TestRerankTruncatesToK(t *testing.T) {
	r := NewReranker(scoreFirstFeature(), 1)
	cands := []Candidate{cand(0, 0.1), cand(1, 0.9), cand(2, 0.5)}
	got, _ := r.Rerank(cands, 2)
	if len(got) != 2 {
		t.Fatalf("k=2 should return 2 results, got %d", len(got))
	}
	if got[0].Doc.Local != 1 || got[1].Doc.Local != 2 {
		t.Fatalf("top-2 wrong: %d then %d", got[0].Doc.Local, got[1].Doc.Local)
	}
}

func TestRerankTieBreak(t *testing.T) {
	r := NewReranker(scoreFirstFeature(), 1)
	// Equal model scores: order must break toward the smaller id deterministically.
	cands := []Candidate{cand(2, 0.5), cand(0, 0.5), cand(1, 0.5)}
	for range 5 {
		got, _ := r.Rerank(cands, 3)
		if got[0].Doc.Local != 0 || got[1].Doc.Local != 1 || got[2].Doc.Local != 2 {
			t.Fatalf("tie break not deterministic: %d %d %d", got[0].Doc.Local, got[1].Doc.Local, got[2].Doc.Local)
		}
	}
}

func TestRerankEmptyAndZeroK(t *testing.T) {
	r := NewReranker(scoreFirstFeature(), 1)
	if got, rr := r.Rerank(nil, 5); got != nil || rr {
		t.Fatalf("empty input should return nil, false; got %v %v", got, rr)
	}
	if got, rr := r.Rerank([]Candidate{cand(0, 1)}, 0); got != nil || rr {
		t.Fatalf("k<=0 should return nil, false; got %v %v", got, rr)
	}
}

func TestNewRerankerDefaultPool(t *testing.T) {
	r := NewReranker(scoreFirstFeature(), 0)
	if r.minPool != DefaultMinRerankPool {
		t.Fatalf("minPool <= 0 should default to %d, got %d", DefaultMinRerankPool, r.minPool)
	}
}
