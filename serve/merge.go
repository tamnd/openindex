package serve

import (
	"container/heap"

	"openindex"
)

// MergeTopK reassembles a global top-k from the per-shard responses. Each shard
// already returns its results ranked, so this is a k-way merge across sorted
// lists rather than a full sort: a max-heap holds the current head of every
// shard, and k pops drain the global best. The order matches the cascade's
// convention, higher score first, ties broken toward the smaller document id so
// the page is deterministic across runs and across which shards happened to
// answer.
//
// A shard whose list is not sorted would still produce a valid set of k
// documents, but the ranking would be wrong; the leaf contract is that a
// response is ranked, which the index/search retrieval guarantees.
func MergeTopK(shards []Response, k int) []openindex.Result {
	if k <= 0 {
		return nil
	}
	h := &mergeHeap{}
	for s := range shards {
		if len(shards[s].Results) > 0 {
			heap.Push(h, cursor{shard: s, pos: 0, results: shards[s].Results})
		}
	}
	out := make([]openindex.Result, 0, k)
	for h.Len() > 0 && len(out) < k {
		top := heap.Pop(h).(cursor)
		out = append(out, top.results[top.pos])
		if top.pos+1 < len(top.results) {
			top.pos++
			heap.Push(h, top)
		}
	}
	return out
}

// cursor is one shard's position in the merge.
type cursor struct {
	shard   int
	pos     int
	results []openindex.Result
}

func (c cursor) head() openindex.Result { return c.results[c.pos] }

// mergeHeap orders cursors by their current head, best first, breaking ties
// toward the smaller document id so the merge is deterministic.
type mergeHeap []cursor

func (h mergeHeap) Len() int { return len(h) }

func (h mergeHeap) Less(i, j int) bool {
	a, b := h[i].head(), h[j].head()
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Doc.Segment != b.Doc.Segment {
		return a.Doc.Segment < b.Doc.Segment
	}
	return a.Doc.Local < b.Doc.Local
}

func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *mergeHeap) Push(x any) { *h = append(*h, x.(cursor)) }

func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
