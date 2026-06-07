// Package ann is the pure-Go HNSW index (architecture doc 06.1, 06.2): the hot
// per-segment approximate-nearest-neighbor graph. Following Weaviate's model
// rather than wrapping an immutable C++ library, it is pure Go so cgo stays off
// the per-query distance path (doc 01) and so the index is mutable and
// rebuildable, which is what the immutable-segment model needs (doc 06.2): one
// graph per segment, a query fans out and merges top-K, and a segment merge
// rebuilds the graph rather than stitching two.
//
// HNSW (Malkov & Yashunin) is a multi-layer proximity graph. Upper layers are
// sparse long-range express lanes; layer 0 holds every node. A search greedily
// descends from a single entry point through the upper layers, then runs a
// bounded best-first search on layer 0. Build quality is governed by M and
// efConstruction; query recall-versus-latency is the per-query efSearch knob
// the serving tier turns under its budget (doc 08).
package ann

import (
	"math"
	"math/rand/v2"
	"sort"

	"openindex/vector"
)

// Params configures graph shape and build/search effort (doc 06.2).
type Params struct {
	// M is the target out-degree at layers above 0; layer 0 allows 2M (Mmax0).
	// Higher M escapes local minima (better recall) at more memory and build
	// time. 16 is the common default.
	M int
	// EfConstruction is the build-time candidate queue. Larger means a better
	// graph at higher build cost; it does not change resident size.
	EfConstruction int
	// EfSearch is the default query-time candidate queue. Raises recall and
	// latency only. Search can override it per query.
	EfSearch int
	// Metric is the distance used for every comparison.
	Metric vector.Metric
	// Seed makes level assignment deterministic for reproducible builds and
	// tests. Zero is a valid seed.
	Seed uint64
}

// withDefaults fills unset fields with the doc 06.2 defaults.
func (p Params) withDefaults() Params {
	if p.M <= 0 {
		p.M = 16
	}
	if p.EfConstruction <= 0 {
		p.EfConstruction = 200
	}
	if p.EfSearch <= 0 {
		p.EfSearch = 50
	}
	return p
}

// node is one indexed vector and its per-layer adjacency. neighbors[l] is the
// out-edge list at layer l; len(neighbors) == its top layer + 1.
type node struct {
	id        uint32
	vec       vector.Vector
	neighbors [][]int // by layer -> internal node indices
}

// HNSW is a mutable HNSW index over vectors of one fixed dimension. It is not
// safe for concurrent writes; concurrent reads of a sealed graph are safe. A
// segment builds one and then only searches it (doc 06.2).
type HNSW struct {
	params Params
	mMax0  int
	mL     float64 // level multiplier, ~1/ln(M)
	rng    *rand.Rand
	nodes  []*node
	entry  int // internal index of the entry point, -1 when empty
	dim    int // fixed dimension, set by the first Add
}

// New creates an empty index with the given parameters.
func New(p Params) *HNSW {
	p = p.withDefaults()
	return &HNSW{
		params: p,
		mMax0:  2 * p.M,
		mL:     1 / math.Log(float64(p.M)),
		rng:    rand.New(rand.NewPCG(p.Seed, p.Seed^0x9e3779b97f4a7c15)),
		entry:  -1,
		dim:    -1,
	}
}

// Len reports the number of indexed vectors.
func (h *HNSW) Len() int { return len(h.nodes) }

// dist is the metric distance between two stored nodes' vectors.
func (h *HNSW) dist(a, b vector.Vector) float32 { return h.params.Metric.Distance(a, b) }

// randomLevel draws a node's top layer from the exponential distribution that
// puts most nodes on layer 0 and exponentially fewer on each layer above,
// minimizing cross-layer neighbor overlap (doc 06.2).
func (h *HNSW) randomLevel() int {
	r := h.rng.Float64()
	if r <= 0 {
		r = math.SmallestNonzeroFloat64
	}
	return int(-math.Log(r) * h.mL)
}

// Add inserts vector v under external id. The vector is cloned, so the caller
// may reuse its slice. The first Add fixes the index dimension; a later vector
// of a different length panics, matching the single-dimension segment contract.
func (h *HNSW) Add(id uint32, v vector.Vector) {
	if h.dim == -1 {
		h.dim = len(v)
	} else if len(v) != h.dim {
		panic(vector.ErrDimMismatch)
	}
	level := h.randomLevel()
	n := &node{id: id, vec: vector.Clone(v), neighbors: make([][]int, level+1)}
	cur := len(h.nodes)
	h.nodes = append(h.nodes, n)

	// First node becomes the entry point and is otherwise unconnected.
	if h.entry == -1 {
		h.entry = cur
		return
	}

	ep := h.entry
	topLevel := len(h.nodes[h.entry].neighbors) - 1

	// Phase 1: greedily descend the layers above the new node with ef=1,
	// carrying the single closest node down as the next layer's entry point.
	for lc := topLevel; lc > level; lc-- {
		ep = h.greedyClosest(n.vec, ep, lc)
	}

	// Phase 2: from min(level, topLevel) down to 0, run the wide search, select
	// neighbors, and wire bidirectional edges with degree pruning.
	for lc := min(level, topLevel); lc >= 0; lc-- {
		cands := h.searchLayer(n.vec, []int{ep}, h.params.EfConstruction, lc)
		mMax := h.params.M
		if lc == 0 {
			mMax = h.mMax0
		}
		selected := h.selectNeighbors(cands, h.params.M)
		n.neighbors[lc] = selected
		for _, m := range selected {
			h.nodes[m].neighbors[lc] = append(h.nodes[m].neighbors[lc], cur)
			// Prune the neighbor back down to its degree budget if it overflowed.
			if len(h.nodes[m].neighbors[lc]) > mMax {
				h.pruneNode(m, lc, mMax)
			}
		}
		if len(cands) > 0 {
			ep = cands[0].node // closest of this layer seeds the next
		}
	}

	// A taller new node becomes the entry point.
	if level > topLevel {
		h.entry = cur
	}
}

// candidate is a node index paired with its distance to the query, the unit the
// search heaps and the selection heuristic pass around.
type candidate struct {
	node int
	dist float32
}

// greedyClosest walks layer lc from ep, repeatedly hopping to the neighbor
// closest to q until no neighbor improves, and returns that local minimum. This
// is the ef=1 descent used for the layers above the search/insert target.
func (h *HNSW) greedyClosest(q vector.Vector, ep, lc int) int {
	best := ep
	bestDist := h.dist(q, h.nodes[ep].vec)
	for {
		improved := false
		for _, nb := range h.nodes[best].neighbors[lc] {
			if d := h.dist(q, h.nodes[nb].vec); d < bestDist {
				best, bestDist, improved = nb, d, true
			}
		}
		if !improved {
			return best
		}
	}
}

// searchLayer is the bounded best-first search of HNSW (Algorithm 2): explore
// from entryPoints, keeping the ef closest found so far. It returns those
// candidates sorted nearest-first. The visited set stops re-expansion; the
// candidate min-heap drives exploration toward the query; the result max-heap
// caps the working set at ef and supplies the early-stop bound.
func (h *HNSW) searchLayer(q vector.Vector, entryPoints []int, ef, lc int) []candidate {
	visited := make(map[int]struct{}, ef*2)
	toVisit := &minDistHeap{}
	found := &maxDistHeap{}
	for _, ep := range entryPoints {
		d := h.dist(q, h.nodes[ep].vec)
		visited[ep] = struct{}{}
		toVisit.push(candidate{ep, d})
		found.push(candidate{ep, d})
	}

	for toVisit.len() > 0 {
		c := toVisit.pop()
		// If the nearest unexplored candidate is farther than the current worst
		// result and the result set is full, no remaining node can improve it.
		if found.len() >= ef && c.dist > found.peek().dist {
			break
		}
		for _, e := range h.nodes[c.node].neighbors[lc] {
			if _, seen := visited[e]; seen {
				continue
			}
			visited[e] = struct{}{}
			d := h.dist(q, h.nodes[e].vec)
			if found.len() < ef || d < found.peek().dist {
				toVisit.push(candidate{e, d})
				found.push(candidate{e, d})
				if found.len() > ef {
					found.pop()
				}
			}
		}
	}

	out := found.drainSorted()
	return out
}

// selectNeighbors is the heuristic edge selection (HNSW Algorithm 4): walk the
// candidates nearest-first and keep one only if it is closer to the query than
// to every already-kept neighbor. This spreads edges across directions instead
// of clustering them, which is what gives HNSW its long-range express edges and
// keeps recall up versus a naive "keep the M nearest". cands must be sorted
// nearest-first (searchLayer returns them so). The query is not a parameter:
// each candidate already carries its distance to the query in cand.dist.
func (h *HNSW) selectNeighbors(cands []candidate, m int) []int {
	result := make([]int, 0, m)
	for _, c := range cands {
		if len(result) >= m {
			break
		}
		keep := true
		for _, r := range result {
			if h.dist(h.nodes[c.node].vec, h.nodes[r].vec) < c.dist {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, c.node)
		}
	}
	return result
}

// pruneNode re-selects an over-degree node's neighbor list down to mMax using
// the same heuristic, so a popular node does not accumulate unbounded edges.
func (h *HNSW) pruneNode(idx, lc, mMax int) {
	n := h.nodes[idx]
	cands := make([]candidate, len(n.neighbors[lc]))
	for i, nb := range n.neighbors[lc] {
		cands[i] = candidate{nb, h.dist(n.vec, h.nodes[nb].vec)}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	n.neighbors[lc] = h.selectNeighbors(cands, mMax)
}

// Search returns the k nearest neighbors of q, nearest first. ef overrides the
// configured EfSearch when positive; it is clamped up to k so the working set
// can always hold the result. An empty index returns nil.
func (h *HNSW) Search(q vector.Vector, k, ef int) []vector.Neighbor {
	if h.entry == -1 || k <= 0 {
		return nil
	}
	if ef <= 0 {
		ef = h.params.EfSearch
	}
	if ef < k {
		ef = k
	}

	ep := h.entry
	topLevel := len(h.nodes[h.entry].neighbors) - 1
	for lc := topLevel; lc > 0; lc-- {
		ep = h.greedyClosest(q, ep, lc)
	}
	cands := h.searchLayer(q, []int{ep}, ef, 0)

	if len(cands) > k {
		cands = cands[:k]
	}
	out := make([]vector.Neighbor, len(cands))
	for i, c := range cands {
		out[i] = vector.Neighbor{ID: h.nodes[c.node].id, Dist: c.dist}
	}
	return out
}
