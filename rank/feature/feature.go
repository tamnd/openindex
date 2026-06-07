// Package feature assembles the learning-to-rank feature vector for a
// query-document pair (architecture doc 07.5). Assembly is the hot second-stage
// operation, so the vector is a single pre-sized []float32 filled by position,
// with no per-feature allocation (impl doc 01.4).
//
// The feature set and its order are versioned together with the model: a
// feature added or moved is a SchemaVersion bump, and the model trained on that
// layout carries the same version, so a feature change and a model change ship
// as one atomic deploy and a stale model can never be fed a vector it does not
// expect.
package feature

// SchemaVersion identifies the feature layout below. The trained model records
// the version it was built against; the serving path refuses a model whose
// version does not match (doc 07.5).
const SchemaVersion = 1

// Feature is a position in the vector. The iota order is the wire order handed
// to the model; do not reorder without bumping SchemaVersion.
type Feature int

const (
	// Index-resident features come straight from the segment (docs 05, 06).
	BM25FScore Feature = iota
	DenseSim
	Proximity
	ExactMatch
	DocLength

	// Document-resident features are precomputed offline and stored in the
	// forward store and meta: family (docs 03, 05), so they are a local read.
	PageRank
	SiteAuthority
	SpamScore
	Freshness

	// Behavioral features come from the aggregated crowd-signal store (doc 11),
	// keyed by query and document and geo-fenced.
	ClickRate
	Dwell

	// NumFeatures is the vector length. It must stay last.
	NumFeatures
)

// Index holds the index-resident inputs.
type Index struct {
	BM25F      float32
	DenseSim   float32
	Proximity  float32
	ExactMatch float32
	DocLength  float32
}

// Document holds the precomputed document-resident inputs.
type Document struct {
	PageRank      float32
	SiteAuthority float32
	SpamScore     float32
	Freshness     float32
}

// Behavioral holds the aggregated crowd-signal inputs.
type Behavioral struct {
	ClickRate float32
	Dwell     float32
}

// Vector is one assembled feature vector. Its length is always NumFeatures.
type Vector []float32

// New returns a zeroed vector of the correct length, ready to fill. Callers
// that score many candidates should allocate one vector per candidate from a
// reused backing array; New is the simple path.
func New() Vector { return make(Vector, NumFeatures) }

// Set writes one feature by position.
func (v Vector) Set(f Feature, x float32) { v[f] = x }

// Get reads one feature by position.
func (v Vector) Get(f Feature) float32 { return v[f] }

// Assemble fills a vector from the three sources. It writes into dst when dst
// has length NumFeatures, reusing the caller's backing array, and allocates a
// fresh vector otherwise. It returns the filled vector.
func Assemble(dst Vector, idx Index, doc Document, beh Behavioral) Vector {
	if len(dst) != int(NumFeatures) {
		dst = New()
	}
	dst[BM25FScore] = idx.BM25F
	dst[DenseSim] = idx.DenseSim
	dst[Proximity] = idx.Proximity
	dst[ExactMatch] = idx.ExactMatch
	dst[DocLength] = idx.DocLength
	dst[PageRank] = doc.PageRank
	dst[SiteAuthority] = doc.SiteAuthority
	dst[SpamScore] = doc.SpamScore
	dst[Freshness] = doc.Freshness
	dst[ClickRate] = beh.ClickRate
	dst[Dwell] = beh.Dwell
	return dst
}
