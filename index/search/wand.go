// Package search is the top-K retrieval loop (indexer doc 05.3): Block-Max WAND
// over a disjunction of term posting lists. WAND maintains a competitive
// threshold (the current k-th best score) and uses each term's score upper
// bound to pick a pivot document — the smallest doc id whose accumulated upper
// bound could beat the threshold — so documents that cannot enter the top-K are
// never scored. Block-Max WAND tightens that with per-block impact bounds
// (index/postings block-max metadata), skipping whole blocks whose maximum
// contribution cannot beat the threshold.
//
// The scorer is a seam: BM25F (doc 07) is the production scorer, and the only
// precondition WAND requires of it is non-negative scores, which BM25F
// satisfies. The linear reference scorer here is enough to exercise and verify
// the skipping logic against an exhaustive scan.
package search

import (
	"container/heap"
	"sort"

	"openindex"
	"openindex/index/postings"
)

// Scorer turns a term frequency into a non-negative score contribution and
// bounds a block's contribution from that block's maximum frequency. The bound
// MUST be >= the score of any document in the block, or WAND will wrongly skip a
// competitive document.
type Scorer interface {
	Score(freq uint32) openindex.Score
	MaxScore(maxFreq uint32) openindex.Score
}

// TermInput is one query term: a cursor over its postings, the scorer for its
// field weight, and the term's global maximum frequency (for its upper bound).
type TermInput struct {
	Cursor  *postings.Cursor
	Scorer  Scorer
	MaxFreq uint32
}

// Hit is a scored document in the result set.
type Hit struct {
	Doc   openindex.DocID
	Score openindex.Score
}

// termState is the per-term mutable retrieval state.
type termState struct {
	cur      *postings.Cursor
	scorer   Scorer
	maxScore openindex.Score // global upper bound = scorer.MaxScore(MaxFreq)
	doc      openindex.DocID
	done     bool
}

func (t *termState) advance(target openindex.DocID) {
	d, ok := t.cur.NextGEQ(target)
	if !ok {
		t.done = true
		return
	}
	t.doc = d
}

// WAND returns the top-k documents of the disjunction of the given terms,
// highest score first. With no terms or k <= 0 it returns nil.
func WAND(terms []TermInput, k int) []Hit {
	if k <= 0 || len(terms) == 0 {
		return nil
	}
	states := make([]*termState, 0, len(terms))
	for _, t := range terms {
		ts := &termState{cur: t.Cursor, scorer: t.Scorer, maxScore: t.Scorer.MaxScore(t.MaxFreq)}
		// Position each cursor on its first document.
		if d, ok := ts.cur.NextGEQ(0); ok {
			ts.doc = d
		} else {
			ts.done = true
		}
		states = append(states, ts)
	}

	top := &minHeap{}
	var threshold openindex.Score // the k-th best score; 0 until the heap is full

	for {
		// Order live terms by current document id.
		live := liveSorted(states)
		if len(live) == 0 {
			break
		}
		// Find the pivot: the first term whose cumulative upper bound exceeds the
		// threshold. Documents below the pivot's doc cannot reach the top-K.
		pivot := findPivot(live, threshold)
		if pivot < 0 {
			break // no document can beat the threshold; done
		}
		pivotDoc := live[pivot].doc

		if live[0].doc == pivotDoc {
			// Block-Max refinement: if the block-level bound at pivotDoc cannot beat
			// the threshold, skip past the shallowest block end instead of scoring.
			if blockMaxSum(live, pivotDoc) <= threshold && threshold > 0 {
				skipBlock(live, pivotDoc)
				continue
			}
			score := scoreDoc(live, pivotDoc)
			threshold = pushTop(top, Hit{Doc: pivotDoc, Score: score}, k, threshold)
			// Advance every term that was sitting on pivotDoc.
			for _, ts := range live {
				if ts.doc == pivotDoc {
					ts.advance(pivotDoc + 1)
				}
			}
		} else {
			// Move a term strictly before pivotDoc up to it so the lists realign.
			// It must be a term whose doc < pivotDoc, or the advance is a no-op and
			// the loop cannot make progress.
			live[pickAdvance(live, pivotDoc)].advance(pivotDoc)
		}
	}

	return drain(top)
}

// liveSorted returns the non-exhausted terms ordered by ascending current doc.
func liveSorted(states []*termState) []*termState {
	live := make([]*termState, 0, len(states))
	for _, s := range states {
		if !s.done {
			live = append(live, s)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].doc < live[j].doc })
	return live
}

// findPivot returns the index in the doc-sorted live terms where the cumulative
// global upper bound first exceeds threshold, or -1 if it never does.
func findPivot(live []*termState, threshold openindex.Score) int {
	var sum openindex.Score
	for i, ts := range live {
		sum += ts.maxScore
		if sum > threshold {
			return i
		}
	}
	return -1
}

// blockMaxSum is the tighter Block-Max bound for pivotDoc: the sum of per-block
// max contributions of the terms whose current doc is <= pivotDoc, using the
// block that AdvanceShallow lands on.
func blockMaxSum(live []*termState, pivotDoc openindex.DocID) openindex.Score {
	var sum openindex.Score
	for _, ts := range live {
		if ts.doc > pivotDoc {
			break
		}
		ts.cur.AdvanceShallow(pivotDoc)
		sum += ts.scorer.MaxScore(ts.cur.BlockMaxFreq())
	}
	return sum
}

// skipBlock jumps the cursors forward when the block-max bound at pivotDoc
// cannot beat the threshold. The safe jump is the smaller of (the shallowest
// block boundary among the pivot-set terms) + 1 and the next term's current doc,
// so it never steps over a document that a term beyond the pivot set could make
// competitive. Only pivot-set terms (doc <= pivotDoc) are advanced.
func skipBlock(live []*termState, pivotDoc openindex.DocID) {
	minLast := openindex.DocID(0)
	first := true
	var nextTermDoc openindex.DocID
	haveNext := false
	for _, ts := range live {
		if ts.doc > pivotDoc {
			nextTermDoc, haveNext = ts.doc, true
			break
		}
		ts.cur.AdvanceShallow(pivotDoc)
		last := ts.cur.BlockLastDoc()
		if first || last < minLast {
			minLast, first = last, false
		}
	}
	target := minLast + 1
	if haveNext && nextTermDoc < target {
		target = nextTermDoc
	}
	for _, ts := range live {
		if ts.doc < target {
			ts.advance(target)
		}
	}
}

// scoreDoc sums the contributions of every term sitting on doc.
func scoreDoc(live []*termState, doc openindex.DocID) openindex.Score {
	var s openindex.Score
	for _, ts := range live {
		if ts.doc == doc {
			s += ts.scorer.Score(ts.cur.Freq())
		}
	}
	return s
}

// pickAdvance chooses which term sitting strictly before pivotDoc to advance.
// Among those it picks the largest upper bound, which tends to realign the lists
// fastest; restricting to doc < pivotDoc is what guarantees the advance makes
// progress (advancing a term already at pivotDoc would be a no-op).
func pickAdvance(live []*termState, pivotDoc openindex.DocID) int {
	best, bestScore := -1, openindex.Score(-1)
	for i, ts := range live {
		if ts.doc < pivotDoc && ts.maxScore > bestScore {
			best, bestScore = i, ts.maxScore
		}
	}
	return best
}

// pushTop inserts hit into the top-k heap and returns the new threshold (the
// k-th best score once the heap is full, else 0). When the heap is full, hit
// replaces the root if it ranks better by (score desc, doc asc) — the same
// tie-break the final ordering uses, so equal-scoring documents resolve toward
// the smaller doc id deterministically.
func pushTop(h *minHeap, hit Hit, k int, threshold openindex.Score) openindex.Score {
	if h.Len() < k {
		heap.Push(h, hit)
	} else if better(hit, (*h)[0]) {
		heap.Pop(h)
		heap.Push(h, hit)
	}
	if h.Len() == k {
		return (*h)[0].Score
	}
	return threshold
}

// better reports whether a outranks b by (score desc, doc asc).
func better(a, b Hit) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Doc < b.Doc
}

// drain empties the heap and returns it in final rank order (score desc, doc
// asc).
func drain(h *minHeap) []Hit {
	out := make([]Hit, h.Len())
	for i := range out {
		out[i] = heap.Pop(h).(Hit)
	}
	sort.Slice(out, func(i, j int) bool { return better(out[i], out[j]) })
	return out
}

// minHeap keeps the current top-K with the weakest hit by (score desc, doc asc)
// at the root, so the root is the first evicted and is also the score that sets
// the competitive threshold.
type minHeap []Hit

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return better(h[j], h[i]) }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(Hit)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
