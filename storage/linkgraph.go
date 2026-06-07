package storage

// LinkGraph is the mutable, incrementally-updated adjacency index (storage doc
// 03.4). It is stored alongside the WebTable, not inside it, and is the half
// the link-inversion pass reads to turn out-links into the WebTable's anchor
// family and the periodic batch job reads to build the WebGraph-compressed
// snapshot for PageRank.
//
// Each link is written twice so both directions are a range scan keyed by
// reversed host (and therefore domain-contiguous):
//
//	forward edge:  'F' | ordered(src) | ordered(dst)   value: anchor text
//	back edge:     'B' | ordered(dst) | ordered(src)   value: anchor text
//
// OutLinks(src) and InLinks(dst) are then a single prefix scan each. The anchor
// text rides on the edge so link inversion has it without a second lookup.
type LinkGraph struct {
	eng Engine
}

// NewLinkGraph wraps an Engine as a link graph. It uses its own key prefixes,
// so it can share an engine with a WebTable or run on a dedicated one.
func NewLinkGraph(eng Engine) *LinkGraph { return &LinkGraph{eng: eng} }

const (
	forwardTag = 'F'
	backTag    = 'B'
)

func forwardKey(src, dst []byte) []byte {
	b := []byte{forwardTag}
	b = appendOrdered(b, src)
	return appendOrdered(b, dst)
}

func backKey(dst, src []byte) []byte {
	b := []byte{backTag}
	b = appendOrdered(b, dst)
	return appendOrdered(b, src)
}

// Edge is one link with its anchor text.
type Edge struct {
	Node   []byte // the other endpoint (dst for OutLinks, src for InLinks)
	Anchor string
}

// AddLink records a link src -> dst with the given anchor text, writing both
// the forward and back edges in one atomic batch so a reader never sees the
// graph half-updated.
func (g *LinkGraph) AddLink(src, dst []byte, anchor string) error {
	var b Batch
	b.Set(forwardKey(src, dst), []byte(anchor))
	b.Set(backKey(dst, src), []byte(anchor))
	return g.eng.Apply(&b)
}

// OutLinks returns the destinations src links to, in destination-key order.
func (g *LinkGraph) OutLinks(src []byte) ([]Edge, error) {
	prefix := appendOrdered([]byte{forwardTag}, src)
	return g.scanEdges(prefix, prefix)
}

// InLinks returns the sources that link to dst, in source-key order. This is
// the scan the link-inversion pass runs to gather a page's inbound anchor text
// for the WebTable anchor family (03.2, 03.4).
func (g *LinkGraph) InLinks(dst []byte) ([]Edge, error) {
	prefix := appendOrdered([]byte{backTag}, dst)
	return g.scanEdges(prefix, prefix)
}

// scanEdges walks a forward/back prefix and decodes the trailing node segment.
func (g *LinkGraph) scanEdges(prefix, nodePrefix []byte) ([]Edge, error) {
	it := g.eng.Scan(prefix, PrefixEnd(prefix))
	defer func() { _ = it.Close() }()
	var out []Edge
	for it.Next() {
		node := decodeOrdered(it.Key()[len(nodePrefix):])
		out = append(out, Edge{Node: node, Anchor: string(it.Value())})
	}
	return out, it.Err()
}

// OutDegree counts src's out-links without materializing them, the cheap form
// the PageRank build wants for its degree pass.
func (g *LinkGraph) OutDegree(src []byte) (int, error) {
	prefix := appendOrdered([]byte{forwardTag}, src)
	it := g.eng.Scan(prefix, PrefixEnd(prefix))
	defer func() { _ = it.Close() }()
	n := 0
	for it.Next() {
		n++
	}
	return n, it.Err()
}
