package bm25f

import "openindex"

// WANDScorer adapts BM25F to the index/search.Scorer seam so BM25F drives the
// first-stage Block-Max WAND retrieval (doc 05.3, doc 07.1). It satisfies the
// seam's two methods:
//
//	Score(freq)        -> the term's contribution for a document with that freq
//	MaxScore(maxFreq)  -> an upper bound over any document the term can reach
//
// Deliberate narrowing. The seam is frequency-only: it never sees the document
// length, so per-document length normalization cannot be applied here. The
// WANDScorer therefore scores at the average document length (B_f collapses to
// 1), which is standard impact-style first-stage scoring: WAND uses these
// bounds only to choose which documents are worth scoring, and exact per-field,
// length-normalized BM25F (the Scorer above) re-scores the survivors in the
// relevance stage. Because the score is monotonic in freq at fixed length,
// MaxScore(maxFreq) is a true upper bound over freq <= maxFreq, which is the one
// property WAND requires for correctness.
type WANDScorer struct {
	idf    float32
	weight float32
	k1     float32
}

// NewWANDScorer builds a first-stage scorer for one query term over one field's
// posting list. idf is the term's IDF, and field selects the per-field weight
// from params (the inverted-index reference carries a single body posting list,
// so this is FieldBody in practice).
func NewWANDScorer(p Params, idf float32, field openindex.Field) WANDScorer {
	return WANDScorer{idf: idf, weight: p.Weights[field], k1: p.K1}
}

// score is the shared saturation at the average document length.
func (w WANDScorer) score(freq uint32) openindex.Score {
	if freq == 0 || w.weight == 0 {
		return 0
	}
	eff := w.weight * float32(freq)
	return openindex.Score(w.idf * eff * (w.k1 + 1) / (w.k1 + eff))
}

// Score returns the term's contribution for a document in which it occurs freq
// times. It implements index/search.Scorer.
func (w WANDScorer) Score(freq uint32) openindex.Score { return w.score(freq) }

// MaxScore returns the term's upper bound given its maximum in-block or global
// frequency. It implements index/search.Scorer.
func (w WANDScorer) MaxScore(maxFreq uint32) openindex.Score { return w.score(maxFreq) }
