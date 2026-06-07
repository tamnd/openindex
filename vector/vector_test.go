package vector

import (
	"math"
	"testing"
)

func approx(a, b, eps float32) bool { return float32(math.Abs(float64(a-b))) <= eps }

func TestL2Distance(t *testing.T) {
	a := Vector{1, 2, 3}
	b := Vector{4, 6, 3}
	// squared: 9 + 16 + 0 = 25
	if got := L2.Distance(a, b); !approx(got, 25, 1e-5) {
		t.Errorf("L2 distance=%v want 25", got)
	}
	if got := L2.Distance(a, a); got != 0 {
		t.Errorf("self distance=%v want 0", got)
	}
}

func TestInnerProductNegated(t *testing.T) {
	a := Vector{1, 2, 3}
	b := Vector{2, 0, 1}
	// dot = 2 + 0 + 3 = 5, returned negated so smaller == more similar
	if got := InnerProduct.Distance(a, b); !approx(got, -5, 1e-5) {
		t.Errorf("inner-product distance=%v want -5", got)
	}
}

func TestCosineNegatedAndNormalize(t *testing.T) {
	a := Vector{3, 0}
	b := Vector{0, 4}
	if got := Cosine.Distance(a, b); !approx(got, 0, 1e-6) {
		t.Errorf("orthogonal cosine distance=%v want 0", got)
	}
	c := Vector{2, 0}
	if got := Cosine.Distance(a, c); !approx(got, -1, 1e-6) {
		t.Errorf("parallel cosine distance=%v want -1", got)
	}
	// After Normalize, InnerProduct equals Cosine on the same pair.
	x := Normalize(Clone(a))
	y := Normalize(Clone(c))
	if got := InnerProduct.Distance(x, y); !approx(got, -1, 1e-6) {
		t.Errorf("normalized inner-product=%v want -1", got)
	}
}

func TestZeroVectorCosine(t *testing.T) {
	if got := Cosine.Distance(Vector{0, 0}, Vector{1, 1}); got != 0 {
		t.Errorf("zero-vector cosine distance=%v want 0 (defined dissimilar)", got)
	}
	z := Vector{0, 0}
	if n := Normalize(z); n[0] != 0 || n[1] != 0 {
		t.Errorf("normalizing zero vector should leave it unchanged, got %v", n)
	}
}

func TestDimMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on dimension mismatch")
		}
	}()
	L2.Distance(Vector{1, 2}, Vector{1, 2, 3})
}

func TestCloneIsIndependent(t *testing.T) {
	a := Vector{1, 2, 3}
	c := Clone(a)
	c[0] = 99
	if a[0] != 1 {
		t.Error("Clone must not alias the source")
	}
}
