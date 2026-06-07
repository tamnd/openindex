package bm25f

import (
	"math"
	"testing"

	"openindex"
	"openindex/index/postings"
	"openindex/index/search"
)

// WANDScorer must satisfy the retrieval seam.
var _ search.Scorer = WANDScorer{}

func tf(body, title, anchor, url uint32) [openindex.NumFields]uint32 {
	var f [openindex.NumFields]uint32
	f[openindex.FieldBody] = body
	f[openindex.FieldTitle] = title
	f[openindex.FieldAnchor] = anchor
	f[openindex.FieldURL] = url
	return f
}

func TestIDFMonotoneAndNonNegative(t *testing.T) {
	c := Collection{N: 1000}
	prev := float32(math.MaxFloat32)
	for df := 1; df <= 1000; df += 50 {
		idf := c.IDF(df)
		if idf < 0 {
			t.Fatalf("IDF(df=%d) = %g, want >= 0 (WAND needs non-negative)", df, idf)
		}
		if idf > prev {
			t.Fatalf("IDF not decreasing: IDF(%d)=%g > previous %g", df, idf, prev)
		}
		prev = idf
	}
	// A term in every document carries almost no information.
	if c.IDF(1000) > c.IDF(1) {
		t.Fatal("a ubiquitous term should have lower IDF than a rare one")
	}
}

func TestSaturation(t *testing.T) {
	s := New(DefaultParams(), Collection{N: 100, AvgFieldLen: avg(10)})
	idf := float32(2)
	var prev openindex.Score
	var prevGain openindex.Score
	for f := uint32(1); f <= 20; f++ {
		sc := s.ScoreTerm(idf, tf(f, 0, 0, 0), lens(10))
		if sc <= prev {
			t.Fatalf("score should increase with frequency: f=%d score=%g not > %g", f, sc, prev)
		}
		gain := sc - prev
		if f > 1 && gain >= prevGain {
			t.Fatalf("marginal gain should shrink (saturation): f=%d gain=%g not < %g", f, gain, prevGain)
		}
		prev, prevGain = sc, gain
	}
}

func TestFieldWeightingBoostsTitle(t *testing.T) {
	s := New(DefaultParams(), Collection{N: 100, AvgFieldLen: avg(10)})
	idf := float32(2)
	inBody := s.ScoreTerm(idf, tf(1, 0, 0, 0), lens(10))
	inTitle := s.ScoreTerm(idf, tf(0, 1, 0, 0), lens(10))
	if inTitle <= inBody {
		t.Fatalf("a title hit should outscore a body hit: title=%g body=%g", inTitle, inBody)
	}
}

func TestLengthNormalizationPenalizesLongDocs(t *testing.T) {
	s := New(DefaultParams(), Collection{N: 100, AvgFieldLen: avg(10)})
	idf := float32(2)
	short := s.ScoreTerm(idf, tf(2, 0, 0, 0), lens(2))
	long := s.ScoreTerm(idf, tf(2, 0, 0, 0), lens(40))
	if short <= long {
		t.Fatalf("same tf in a shorter doc should score higher: short=%g long=%g", short, long)
	}
}

func TestPseudoFreqIsCombinedNotSummedPerField(t *testing.T) {
	// The whole point of BM25F: saturation applied once to a combined
	// frequency, not per field. A term split across body and title must score
	// less than the same total mass would if each field saturated alone.
	s := New(DefaultParams(), Collection{N: 100, AvgFieldLen: avg(10)})
	idf := float32(2)
	combined := s.ScoreTerm(idf, tf(5, 5, 0, 0), lens(10))

	// Per-field-summed straw man: saturate body and title independently.
	bodyOnly := s.ScoreTerm(idf, tf(5, 0, 0, 0), lens(10))
	titleOnly := s.ScoreTerm(idf, tf(0, 5, 0, 0), lens(10))
	summed := bodyOnly + titleOnly
	if !(combined < summed) {
		t.Fatalf("combined pseudo-frequency must saturate below per-field sum: combined=%g summed=%g", combined, summed)
	}
}

func TestScoreSumsOverTerms(t *testing.T) {
	s := New(DefaultParams(), Collection{N: 100, AvgFieldLen: avg(10)})
	terms := []TermStats{
		{IDF: 2, TF: tf(3, 0, 0, 0)},
		{IDF: 1, TF: tf(1, 1, 0, 0)},
	}
	want := s.ScoreTerm(2, tf(3, 0, 0, 0), lens(10)) + s.ScoreTerm(1, tf(1, 1, 0, 0), lens(10))
	got := s.Score(terms, lens(10))
	if math.Abs(float64(got-want)) > 1e-5 {
		t.Fatalf("Score = %g, want sum of per-term = %g", got, want)
	}
}

func TestWANDScorerBoundDominatesScore(t *testing.T) {
	// The one property WAND requires: MaxScore(maxFreq) >= Score(freq) for every
	// freq <= maxFreq, or the skip logic drops a competitive document.
	w := NewWANDScorer(DefaultParams(), 2.5, openindex.FieldBody)
	const maxFreq = 64
	bound := w.MaxScore(maxFreq)
	for f := uint32(0); f <= maxFreq; f++ {
		if w.Score(f) > bound {
			t.Fatalf("Score(%d)=%g exceeds MaxScore(%d)=%g", f, w.Score(f), maxFreq, bound)
		}
	}
	if w.Score(0) != 0 {
		t.Fatalf("a term that does not occur should score 0, got %g", w.Score(0))
	}
}

// TestWANDRetrievalMatchesBruteForce drives the real Block-Max WAND loop with
// the BM25F seam and checks the top-k against an exhaustive scan that scores
// every document with the same scorer. This proves the scorer plugs into the
// retrieval path and that the skipping never drops a top-k document.
func TestWANDRetrievalMatchesBruteForce(t *testing.T) {
	// Three terms over a shared doc space. (doc, freq) pairs per term.
	termDocs := [][]openindex.Posting{
		{{Doc: 0, Frequency: 3}, {Doc: 2, Frequency: 1}, {Doc: 5, Frequency: 7}, {Doc: 9, Frequency: 2}},
		{{Doc: 1, Frequency: 5}, {Doc: 2, Frequency: 4}, {Doc: 5, Frequency: 1}, {Doc: 8, Frequency: 9}},
		{{Doc: 0, Frequency: 1}, {Doc: 3, Frequency: 6}, {Doc: 5, Frequency: 2}, {Doc: 9, Frequency: 4}},
	}
	idfs := []float32{1.5, 2.0, 1.0}

	terms := make([]search.TermInput, len(termDocs))
	scorers := make([]WANDScorer, len(termDocs))
	for i, ps := range termDocs {
		list, err := postings.Encode(ps)
		if err != nil {
			t.Fatalf("encode term %d: %v", i, err)
		}
		scorers[i] = NewWANDScorer(DefaultParams(), idfs[i], openindex.FieldBody)
		terms[i] = search.TermInput{Cursor: list.Cursor(), Scorer: scorers[i], MaxFreq: list.MaxFreq()}
	}

	const k = 3
	got := search.WAND(terms, k)

	// Brute force: sum each term's scorer over the documents it covers.
	bf := map[openindex.DocID]openindex.Score{}
	for i, ps := range termDocs {
		for _, p := range ps {
			bf[p.Doc] += scorers[i].Score(p.Frequency)
		}
	}
	want := topK(bf, k)

	if len(got) != len(want) {
		t.Fatalf("WAND returned %d hits, brute force has %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Doc != want[i].doc {
			t.Fatalf("rank %d: WAND doc %d, brute force doc %d", i, got[i].Doc, want[i].doc)
		}
		if math.Abs(float64(got[i].Score-want[i].score)) > 1e-5 {
			t.Fatalf("rank %d doc %d: WAND score %g, brute force %g", i, got[i].Doc, got[i].Score, want[i].score)
		}
	}
}

type scored struct {
	doc   openindex.DocID
	score openindex.Score
}

func topK(m map[openindex.DocID]openindex.Score, k int) []scored {
	all := make([]scored, 0, len(m))
	for d, s := range m {
		all = append(all, scored{d, s})
	}
	// Highest score first, ties toward the smaller doc id (WAND's order).
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].score > all[i].score || (all[j].score == all[i].score && all[j].doc < all[i].doc) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if k < len(all) {
		all = all[:k]
	}
	return all
}

func avg(n float32) [openindex.NumFields]float32 {
	var a [openindex.NumFields]float32
	for i := range a {
		a[i] = n
	}
	return a
}

func lens(n uint32) [openindex.NumFields]uint32 {
	var l [openindex.NumFields]uint32
	for i := range l {
		l[i] = n
	}
	return l
}
