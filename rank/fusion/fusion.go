// Package fusion combines candidate lists from different first-stage retrievers
// into one ranked list (architecture doc 07.4). The lexical retriever (BM25F
// over the inverted index) and the dense retriever (ANN over the vector index)
// produce scores on incompatible scales, so the default fusion is Reciprocal
// Rank Fusion, which throws the scores away and fuses by rank:
//
//	RRF(d) = Sum_lists  1 / (k + rank_list(d)),   k ~ 60
//
// RRF is robust and has no per-list tuning, which is why it is the default. A
// calibrated convex combination (alpha*dense + (1-alpha)*sparse after score
// normalization) can edge it out when the scores are actually calibrated, and
// it is offered here as an option.
package fusion

import (
	"sort"

	"openindex"
)

// DefaultK is the RRF rank constant. The value damps the contribution of a
// document's exact rank so that being near the top of any list matters more
// than the precise position; k=60 is the value from the original RRF paper and
// the common default.
const DefaultK = 60

// Candidate is one retrieved document with its position in a source list. Score
// is the retriever's raw score, used only by the convex-combination fusion;
// RRF ignores it and uses Rank.
type Candidate struct {
	Doc   openindex.GlobalDocID
	Score openindex.Score
	// Rank is the 0-based position in the source list (0 is the top result).
	Rank int
}

// Fused is one document after fusion, with its combined score, sorted highest
// first.
type Fused struct {
	Doc   openindex.GlobalDocID
	Score openindex.Score
}

// RRF fuses ranked lists by reciprocal rank with constant k. A document present
// in several lists accumulates a contribution from each. Lists need not be the
// same length or contain the same documents. With k <= 0 the default is used.
// The result is sorted by fused score descending, ties broken by the document's
// segment then local id so the order is deterministic.
func RRF(k int, lists ...[]Candidate) []Fused {
	if k <= 0 {
		k = DefaultK
	}
	acc := make(map[openindex.GlobalDocID]openindex.Score)
	for _, list := range lists {
		for _, c := range list {
			acc[c.Doc] += openindex.Score(1.0 / float64(k+c.Rank+1))
		}
	}
	return sorted(acc)
}

// Linear fuses lists by a weighted sum of per-list min-max normalized scores:
// the convex combination of doc 07.4. weights[i] is applied to lists[i]; a
// document missing from a list contributes 0 from that list. Each list is
// normalized to [0,1] independently before weighting so the weights mean what
// they say regardless of the raw score scales. Lists beyond the supplied
// weights, or with a zero weight, are ignored.
func Linear(weights []float32, lists ...[]Candidate) []Fused {
	acc := make(map[openindex.GlobalDocID]openindex.Score)
	for i, list := range lists {
		if i >= len(weights) || weights[i] == 0 || len(list) == 0 {
			continue
		}
		lo, hi := scoreRange(list)
		span := hi - lo
		for _, c := range list {
			var norm openindex.Score
			if span > 0 {
				norm = (c.Score - lo) / span
			} else {
				// Every score equal: treat the list as uniformly relevant.
				norm = 1
			}
			acc[c.Doc] += openindex.Score(weights[i]) * norm
		}
	}
	return sorted(acc)
}

// scoreRange returns the min and max raw score in a non-empty list.
func scoreRange(list []Candidate) (lo, hi openindex.Score) {
	lo, hi = list[0].Score, list[0].Score
	for _, c := range list[1:] {
		if c.Score < lo {
			lo = c.Score
		}
		if c.Score > hi {
			hi = c.Score
		}
	}
	return lo, hi
}

// sorted turns the accumulator into a score-descending, deterministically
// tie-broken slice.
func sorted(acc map[openindex.GlobalDocID]openindex.Score) []Fused {
	out := make([]Fused, 0, len(acc))
	for doc, score := range acc {
		out = append(out, Fused{Doc: doc, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Doc.Segment != out[j].Doc.Segment {
			return out[i].Doc.Segment < out[j].Doc.Segment
		}
		return out[i].Doc.Local < out[j].Doc.Local
	})
	return out
}

// FromResults builds a Candidate list from an ordered Result slice, assigning
// ranks by position. It is the adapter from a retriever's output (doc 08) to
// the fusion input.
func FromResults(results []openindex.Result) []Candidate {
	cands := make([]Candidate, len(results))
	for i, r := range results {
		cands[i] = Candidate{Doc: r.Doc, Score: r.Score, Rank: i}
	}
	return cands
}
