// Package vector holds the shared vocabulary of the dense-retrieval subsystem
// (architecture doc 06): the vector type, the distance metric, the pure-Go
// distance kernels, and the neighbor result. It is the leaf of the vector
// package group — ann, quantize, and embed all depend on it — so the distance
// math lives in exactly one place and every component scores the same way.
//
// The distance kernels are deliberately pure Go. Doc 01's native-boundary
// policy forbids cgo on the per-vector path: a cgo call is about 40 ns of
// overhead, which is ruinous inside a candidate sweep that does a handful of
// nanoseconds of arithmetic per dimension. cgo is reserved for the
// segment-build batch boundary (doc 06.1), never here.
package vector

import (
	"errors"
	"math"
)

// Vector is a dense embedding in row-major float32. Length is the dimension;
// the subsystem stores them by the thousands per segment, so float32 (not
// float64) is the storage and arithmetic width — it halves memory at no
// measurable recall cost for retrieval.
type Vector []float32

// Metric selects how two vectors are compared. The convention across the
// subsystem is that a SMALLER distance means MORE similar, so inner-product and
// cosine are returned negated — a max-inner-product search becomes a
// min-distance search and the same top-K min-heap serves every metric.
type Metric uint8

const (
	// L2 is squared Euclidean distance. The square root is monotonic and never
	// needed for ranking, so it is skipped (the ANN sweep only orders).
	L2 Metric = iota
	// InnerProduct is the negated dot product, for maximum-inner-product search
	// (the dense web retriever, doc 06.5). Inputs are assumed unnormalized.
	InnerProduct
	// Cosine is the negated cosine similarity. Equivalent to InnerProduct on
	// L2-normalized inputs; Normalize is the cheap way to get there once.
	Cosine
)

// ErrDimMismatch is returned when two vectors of different length are compared.
var ErrDimMismatch = errors.New("vector: dimension mismatch")

// Neighbor is one result of a similarity search: a vector's id and its distance
// under the active metric. Smaller Dist ranks first.
type Neighbor struct {
	ID   uint32
	Dist float32
}

// Distance returns the distance between a and b under m. It panics on a
// dimension mismatch, matching the contract of an index that only ever holds
// vectors of one dimension; callers that accept untrusted input validate with
// SameDim first.
func (m Metric) Distance(a, b Vector) float32 {
	switch m {
	case InnerProduct:
		return -dot(a, b)
	case Cosine:
		return -cosine(a, b)
	default:
		return l2sq(a, b)
	}
}

// SameDim reports whether a and b share a dimension, the precondition every
// distance kernel assumes.
func SameDim(a, b Vector) bool { return len(a) == len(b) }

// l2sq is the squared Euclidean distance. Writing the loop over a fixed-stride
// slice lets the compiler hoist the bounds check and keeps the body
// allocation-free, which is what makes the pure-Go choice viable on the hot
// path.
func l2sq(a, b Vector) float32 {
	if len(a) != len(b) {
		panic(ErrDimMismatch)
	}
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// dot is the inner product of a and b.
func dot(a, b Vector) float32 {
	if len(a) != len(b) {
		panic(ErrDimMismatch)
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// cosine is the cosine similarity, guarding the zero-norm case (a zero vector
// is defined as maximally dissimilar, similarity 0).
func cosine(a, b Vector) float32 {
	if len(a) != len(b) {
		panic(ErrDimMismatch)
	}
	var d, na, nb float32
	for i := range a {
		d += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return d / float32(math.Sqrt(float64(na))*math.Sqrt(float64(nb)))
}

// Normalize scales v to unit L2 length in place and returns it, so a later
// Cosine query reduces to InnerProduct. A zero vector is left unchanged.
func Normalize(v Vector) Vector {
	var n float32
	for _, x := range v {
		n += x * x
	}
	if n == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(float64(n)))
	for i := range v {
		v[i] *= inv
	}
	return v
}

// Clone returns an independent copy of v, used when an index must retain a
// vector the caller may mutate.
func Clone(v Vector) Vector {
	cp := make(Vector, len(v))
	copy(cp, v)
	return cp
}
