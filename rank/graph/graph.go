// Package graph computes link-authority scores over the WebGraph snapshot
// (architecture doc 07.3): PageRank and its trust-biased variants TrustRank and
// Anti-TrustRank. It is an offline batch job in the ranking path; the resulting
// scores are document-resident features into the LTR model (doc 07.5), never
// the ranking by themselves, and they are gated behind the spam score so a link
// farm cannot drive them.
//
// The executor follows the BSP (Pregel) model: computation proceeds in
// supersteps, in each of which every vertex receives the messages sent to it in
// the previous superstep, recomputes its value, and sends new messages along
// its out-edges. A combiner sums the messages destined for the same vertex at
// send time, because PageRank needs only their sum, and an aggregator reduces a
// global residual each superstep to detect convergence. The canonical example
// converges in about 30 supersteps; web scale takes roughly 45 to 52.
package graph

// Graph is a directed link graph over dense vertex ids 0..N-1. A vertex is a
// page (or a host, for host-level authority); an edge u->v is a link from u to
// v. Out-edges are stored per source for the forward push; the reverse view is
// built on demand for backward propagation.
type Graph struct {
	out [][]int32
}

// New returns a graph with n vertices and no edges.
func New(n int) *Graph {
	return &Graph{out: make([][]int32, n)}
}

// NumVertices returns the vertex count.
func (g *Graph) NumVertices() int { return len(g.out) }

// AddEdge records a link from -> to. Both ids must be in range. Duplicate edges
// are kept: a page that links to another twice pushes twice the mass, matching
// the raw link graph; de-duplication, if wanted, happens when the graph is
// built.
func (g *Graph) AddEdge(from, to int) {
	g.out[from] = append(g.out[from], int32(to))
}

// OutDegree returns the number of out-edges of v.
func (g *Graph) OutDegree(v int) int { return len(g.out[v]) }

// Reverse returns a new graph with every edge direction flipped. Backward
// propagation (Anti-TrustRank, where distrust flows from spam pages to the
// pages that link to them) is forward propagation on the reverse graph.
func (g *Graph) Reverse() *Graph {
	r := New(len(g.out))
	for from := range g.out {
		for _, to := range g.out[from] {
			r.out[to] = append(r.out[to], int32(from))
		}
	}
	return r
}
