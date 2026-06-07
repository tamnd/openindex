package ann

import "sort"

// The HNSW layer search needs two priority queues over candidates: a min-heap
// that always yields the candidate closest to the query (the next to expand),
// and a max-heap that always yields the farthest of the ef best found so far
// (the one to evict and the early-stop bound). Both are tiny, hot, and called
// in the inner loop, so they are hand-rolled on a slice with explicit sift
// rather than going through container/heap's interface dispatch and any-boxing.

// minDistHeap is a binary min-heap of candidates keyed on distance.
type minDistHeap struct{ a []candidate }

func (h *minDistHeap) len() int { return len(h.a) }

func (h *minDistHeap) push(c candidate) {
	h.a = append(h.a, c)
	i := len(h.a) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h.a[p].dist <= h.a[i].dist {
			break
		}
		h.a[p], h.a[i] = h.a[i], h.a[p]
		i = p
	}
}

func (h *minDistHeap) pop() candidate {
	top := h.a[0]
	n := len(h.a) - 1
	h.a[0] = h.a[n]
	h.a = h.a[:n]
	h.siftDown(0)
	return top
}

func (h *minDistHeap) siftDown(i int) {
	n := len(h.a)
	for {
		l, r, small := 2*i+1, 2*i+2, i
		if l < n && h.a[l].dist < h.a[small].dist {
			small = l
		}
		if r < n && h.a[r].dist < h.a[small].dist {
			small = r
		}
		if small == i {
			return
		}
		h.a[i], h.a[small] = h.a[small], h.a[i]
		i = small
	}
}

// maxDistHeap is a binary max-heap of candidates keyed on distance.
type maxDistHeap struct{ a []candidate }

func (h *maxDistHeap) len() int        { return len(h.a) }
func (h *maxDistHeap) peek() candidate { return h.a[0] }

func (h *maxDistHeap) push(c candidate) {
	h.a = append(h.a, c)
	i := len(h.a) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h.a[p].dist >= h.a[i].dist {
			break
		}
		h.a[p], h.a[i] = h.a[i], h.a[p]
		i = p
	}
}

func (h *maxDistHeap) pop() candidate {
	top := h.a[0]
	n := len(h.a) - 1
	h.a[0] = h.a[n]
	h.a = h.a[:n]
	h.siftDown(0)
	return top
}

func (h *maxDistHeap) siftDown(i int) {
	n := len(h.a)
	for {
		l, r, large := 2*i+1, 2*i+2, i
		if l < n && h.a[l].dist > h.a[large].dist {
			large = l
		}
		if r < n && h.a[r].dist > h.a[large].dist {
			large = r
		}
		if large == i {
			return
		}
		h.a[i], h.a[large] = h.a[large], h.a[i]
		i = large
	}
}

// drainSorted returns the heap's candidates sorted nearest-first, which is the
// order both selectNeighbors and Search expect. It consumes the heap.
func (h *maxDistHeap) drainSorted() []candidate {
	out := h.a
	h.a = nil
	sort.Slice(out, func(i, j int) bool { return out[i].dist < out[j].dist })
	return out
}
