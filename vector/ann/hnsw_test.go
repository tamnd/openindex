package ann

import (
	"math/rand/v2"
	"sort"
	"testing"

	"openindex/vector"
)

// bruteForce returns the exact k nearest ids to q, the oracle the approximate
// graph is graded against.
func bruteForce(data []vector.Vector, q vector.Vector, m vector.Metric, k int) []uint32 {
	type nd struct {
		id uint32
		d  float32
	}
	all := make([]nd, len(data))
	for i, v := range data {
		all[i] = nd{uint32(i), m.Distance(q, v)}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].d < all[j].d })
	out := make([]uint32, 0, k)
	for i := 0; i < k && i < len(all); i++ {
		out = append(out, all[i].id)
	}
	return out
}

func randData(n, dim int, seed uint64) []vector.Vector {
	rng := rand.New(rand.NewPCG(seed, seed*2+1))
	data := make([]vector.Vector, n)
	for i := range data {
		v := make(vector.Vector, dim)
		for d := range v {
			v[d] = float32(rng.NormFloat64())
		}
		data[i] = v
	}
	return data
}

func recallAt(got []vector.Neighbor, want []uint32) float64 {
	truth := make(map[uint32]struct{}, len(want))
	for _, id := range want {
		truth[id] = struct{}{}
	}
	hit := 0
	for _, g := range got {
		if _, ok := truth[g.ID]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

func TestHNSWRecallL2(t *testing.T) {
	const n, dim, k = 1000, 32, 10
	data := randData(n, dim, 7)
	h := New(Params{M: 16, EfConstruction: 200, EfSearch: 64, Metric: vector.L2, Seed: 42})
	for i, v := range data {
		h.Add(uint32(i), v)
	}
	if h.Len() != n {
		t.Fatalf("Len=%d want %d", h.Len(), n)
	}

	queries := randData(50, dim, 99)
	var total float64
	for _, q := range queries {
		got := h.Search(q, k, 0)
		if len(got) != k {
			t.Fatalf("Search returned %d hits, want %d", len(got), k)
		}
		// Distances must be non-decreasing (nearest first).
		for i := 1; i < len(got); i++ {
			if got[i].Dist < got[i-1].Dist {
				t.Fatalf("results not sorted: %v", got)
			}
		}
		total += recallAt(got, bruteForce(data, q, vector.L2, k))
	}
	avg := total / float64(len(queries))
	if avg < 0.90 {
		t.Errorf("recall@%d = %.3f, want >= 0.90", k, avg)
	}
}

func TestHNSWInnerProduct(t *testing.T) {
	const n, dim, k = 500, 24, 10
	data := randData(n, dim, 3)
	h := New(Params{M: 16, EfConstruction: 200, EfSearch: 80, Metric: vector.InnerProduct, Seed: 5})
	for i, v := range data {
		h.Add(uint32(i), v)
	}
	queries := randData(30, dim, 11)
	var total float64
	for _, q := range queries {
		total += recallAt(h.Search(q, k, 0), bruteForce(data, q, vector.InnerProduct, k))
	}
	if avg := total / float64(len(queries)); avg < 0.85 {
		t.Errorf("inner-product recall@%d = %.3f, want >= 0.85", k, avg)
	}
}

func TestHNSWDeterministicWithSeed(t *testing.T) {
	data := randData(200, 16, 1)
	build := func() []vector.Neighbor {
		h := New(Params{M: 12, EfConstruction: 100, EfSearch: 32, Metric: vector.L2, Seed: 123})
		for i, v := range data {
			h.Add(uint32(i), v)
		}
		return h.Search(data[0], 10, 0)
	}
	a, b := build(), build()
	if len(a) != len(b) {
		t.Fatalf("result lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed produced different results at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestHNSWEmptyAndEdges(t *testing.T) {
	h := New(Params{Metric: vector.L2, Seed: 1})
	if got := h.Search(vector.Vector{1, 2}, 5, 0); got != nil {
		t.Errorf("empty index should return nil, got %v", got)
	}
	h.Add(0, vector.Vector{1, 1})
	if got := h.Search(vector.Vector{1, 1}, 0, 0); got != nil {
		t.Errorf("k<=0 should return nil, got %v", got)
	}
	got := h.Search(vector.Vector{1, 1}, 5, 0)
	if len(got) != 1 || got[0].ID != 0 {
		t.Errorf("single-node search wrong: %v", got)
	}
}

func TestHNSWDimMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic adding a wrong-dimension vector")
		}
	}()
	h := New(Params{Metric: vector.L2})
	h.Add(0, vector.Vector{1, 2, 3})
	h.Add(1, vector.Vector{1, 2})
}
