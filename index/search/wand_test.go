package search

import (
	"math/rand"
	"sort"
	"testing"

	"openindex"
	"openindex/index/postings"
)

// linearScorer scores a term as weight*freq, with an exact block bound of
// weight*maxFreq. It is non-negative and monotone, so it is a valid WAND scorer.
type linearScorer struct{ weight openindex.Score }

func (s linearScorer) Score(freq uint32) openindex.Score { return s.weight * openindex.Score(freq) }
func (s linearScorer) MaxScore(maxFreq uint32) openindex.Score {
	return s.weight * openindex.Score(maxFreq)
}

// brute scores the disjunction exhaustively and returns the top-k, the oracle
// WAND must match. Tie-break is (score desc, doc asc), matching WAND's
// ascending-doc processing with strict eviction.
func brute(lists []*postings.List, scorers []linearScorer, k int) []Hit {
	agg := map[openindex.DocID]openindex.Score{}
	for i, l := range lists {
		c := l.Cursor()
		for c.Next() {
			agg[c.Doc()] += scorers[i].Score(c.Freq())
		}
	}
	hits := make([]Hit, 0, len(agg))
	for d, s := range agg {
		hits = append(hits, Hit{Doc: d, Score: s})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Doc < hits[j].Doc
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

func buildList(t *testing.T, docs []openindex.DocID, freqs []uint32) *postings.List {
	t.Helper()
	ps := make([]openindex.Posting, len(docs))
	for i := range docs {
		ps[i] = openindex.Posting{Doc: docs[i], Frequency: freqs[i]}
	}
	l, err := postings.Encode(ps)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func inputs(lists []*postings.List, scorers []linearScorer) []TermInput {
	in := make([]TermInput, len(lists))
	for i, l := range lists {
		in[i] = TermInput{Cursor: l.Cursor(), Scorer: scorers[i], MaxFreq: l.MaxFreq()}
	}
	return in
}

func sameHits(a, b []Hit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWANDMatchesBruteForceSmall(t *testing.T) {
	l1 := buildList(t, []openindex.DocID{1, 3, 5, 7, 9}, []uint32{2, 1, 5, 1, 3})
	l2 := buildList(t, []openindex.DocID{2, 3, 5, 8}, []uint32{4, 2, 1, 7})
	scorers := []linearScorer{{weight: 1.0}, {weight: 2.0}}
	lists := []*postings.List{l1, l2}

	for k := 1; k <= 6; k++ {
		got := WAND(inputs(lists, scorers), k)
		want := brute(lists, scorers, k)
		if !sameHits(got, want) {
			t.Errorf("k=%d\n got %+v\nwant %+v", k, got, want)
		}
	}
}

// TestWANDMatchesBruteForceRandom is the property test: across many random
// multi-block lists, Block-Max WAND must return exactly the exhaustive top-k.
func TestWANDMatchesBruteForceRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := range 200 {
		numTerms := 2 + rng.Intn(3)
		lists := make([]*postings.List, numTerms)
		scorers := make([]linearScorer, numTerms)
		for ti := range numTerms {
			n := 1 + rng.Intn(400) // some lists cross block boundaries
			var docs []openindex.DocID
			var freqs []uint32
			var d openindex.DocID
			for range n {
				d += openindex.DocID(1 + rng.Intn(5))
				docs = append(docs, d)
				freqs = append(freqs, uint32(1+rng.Intn(20)))
			}
			lists[ti] = buildList(t, docs, freqs)
			scorers[ti] = linearScorer{weight: openindex.Score(1 + rng.Intn(4))}
		}
		k := 1 + rng.Intn(10)
		got := WAND(inputs(lists, scorers), k)
		want := brute(lists, scorers, k)
		if !sameHits(got, want) {
			t.Fatalf("trial %d k=%d mismatch\n got %+v\nwant %+v", trial, k, got, want)
		}
	}
}

func TestWANDEdgeCases(t *testing.T) {
	if WAND(nil, 5) != nil {
		t.Error("no terms should return nil")
	}
	l := buildList(t, []openindex.DocID{1, 2}, []uint32{1, 1})
	if WAND(inputs([]*postings.List{l}, []linearScorer{{weight: 1}}), 0) != nil {
		t.Error("k=0 should return nil")
	}
}
