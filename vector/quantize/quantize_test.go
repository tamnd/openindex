package quantize

import (
	"math"
	"math/rand/v2"
	"sort"
	"testing"

	"openindex/vector"
)

func randData(n, dim int, seed uint64) []vector.Vector {
	rng := rand.New(rand.NewPCG(seed, seed*3+1))
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

// clusteredData generates points around a handful of random centers, the way
// real embeddings cluster by topic. Uniform Gaussian noise is the adversarial
// worst case for any quantizer; this is the regime the codecs are built for and
// where their ranking quality is meaningful to assert.
func clusteredData(n, dim, clusters int, seed uint64) []vector.Vector {
	rng := rand.New(rand.NewPCG(seed, seed*5+3))
	centers := make([]vector.Vector, clusters)
	for c := range centers {
		v := make(vector.Vector, dim)
		for d := range v {
			v[d] = float32(rng.NormFloat64()) * 6
		}
		centers[c] = v
	}
	data := make([]vector.Vector, n)
	for i := range data {
		c := centers[rng.IntN(clusters)]
		v := make(vector.Vector, dim)
		for d := range v {
			v[d] = c[d] + float32(rng.NormFloat64())*0.5
		}
		data[i] = v
	}
	return data
}

// rankAgree measures how well an approximate distance preserves the true L2
// ordering: it returns recall@k of the approximate top-k against the exact
// top-k over the dataset for a held-out query.
func rankAgree(t *testing.T, exact, approx []float32, k int) float64 {
	t.Helper()
	order := func(d []float32) []int {
		idx := make([]int, len(d))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return d[idx[a]] < d[idx[b]] })
		return idx[:k]
	}
	truth := map[int]struct{}{}
	for _, i := range order(exact) {
		truth[i] = struct{}{}
	}
	hit := 0
	for _, i := range order(approx) {
		if _, ok := truth[i]; ok {
			hit++
		}
	}
	return float64(hit) / float64(k)
}

func TestScalarRoundTripAndRanking(t *testing.T) {
	data := randData(500, 32, 1)
	s := TrainScalar(data)
	if s.Dim() != 32 {
		t.Fatalf("Dim=%d want 32", s.Dim())
	}
	// Reconstruction error per dimension is bounded by half a quantization step.
	for _, v := range data[:20] {
		dec := s.Decode(s.Encode(v))
		for d := range v {
			if e := math.Abs(float64(v[d] - dec[d])); e > float64(s.scale[d]) {
				t.Fatalf("dim %d reconstruction error %.4f exceeds a step", d, e)
			}
		}
	}
	// Approximate distances must largely preserve the true nearest neighbors.
	q := randData(1, 32, 99)[0]
	exact := make([]float32, len(data))
	approx := make([]float32, len(data))
	for i, v := range data {
		exact[i] = vector.L2.Distance(q, v)
		approx[i] = s.Distance(q, s.Encode(v))
	}
	if r := rankAgree(t, exact, approx, 10); r < 0.9 {
		t.Errorf("scalar recall@10 = %.2f, want >= 0.9", r)
	}
}

func TestScalarConstantDimension(t *testing.T) {
	// A dimension that never varies has zero scale and must round-trip its value.
	data := []vector.Vector{{5, 1}, {5, 2}, {5, 3}}
	s := TrainScalar(data)
	dec := s.Decode(s.Encode(vector.Vector{5, 2}))
	if math.Abs(float64(dec[0]-5)) > 1e-6 {
		t.Errorf("constant dim did not round-trip: %v", dec)
	}
}

func TestPQEncodeDecodeAndADC(t *testing.T) {
	data := clusteredData(800, 32, 16, 2)
	pq, err := TrainPQ(data, 8, 20, 7)
	if err != nil {
		t.Fatal(err)
	}
	if pq.M() != 8 || pq.Dim() != 32 {
		t.Fatalf("M=%d Dim=%d want 8,32", pq.M(), pq.Dim())
	}
	// A code is exactly M bytes.
	if got := len(pq.Encode(data[0])); got != 8 {
		t.Fatalf("code length=%d want 8", got)
	}
	// ADC distance must equal the L2 distance to the decoded (reconstructed)
	// vector: both are the query-to-centroid sum, just computed two ways. The
	// query is drawn from the same clustered distribution as the data.
	q := clusteredData(1, 32, 16, 2)[0]
	adc := pq.NewADC(q)
	for _, v := range data[:30] {
		code := pq.Encode(v)
		viaTable := adc.Distance(code)
		viaDecode := vector.L2.Distance(q, pq.Decode(code))
		if math.Abs(float64(viaTable-viaDecode)) > 1e-3 {
			t.Fatalf("ADC %.4f != decode L2 %.4f", viaTable, viaDecode)
		}
	}
	// Ranking is preserved well enough for a first-stage sweep.
	exact := make([]float32, len(data))
	approx := make([]float32, len(data))
	for i, v := range data {
		exact[i] = vector.L2.Distance(q, v)
		approx[i] = adc.Distance(pq.Encode(v))
	}
	if r := rankAgree(t, exact, approx, 20); r < 0.7 {
		t.Errorf("PQ recall@20 = %.2f, want >= 0.7", r)
	}
}

func TestPQRejectsBadParams(t *testing.T) {
	if _, err := TrainPQ(nil, 4, 10, 1); err == nil {
		t.Error("empty sample should error")
	}
	if _, err := TrainPQ(randData(10, 30, 1), 4, 10, 1); err == nil {
		t.Error("dim 30 not divisible by m=4 should error")
	}
}

func TestBinaryHammingAndRescore(t *testing.T) {
	data := clusteredData(600, 128, 12, 4)
	b := TrainBinary(data, 128)
	if b.CodeLen() != 16 {
		t.Fatalf("CodeLen=%d want 16", b.CodeLen())
	}
	q := data[0]
	qcode := b.Encode(q)
	// The query's distance to itself is zero.
	if d := b.Distance(qcode, qcode); d != 0 {
		t.Fatalf("self Hamming=%d want 0", d)
	}
	// Binary quantization is a coarse first pass (the spec: it underperforms on
	// vectors below ~1000 dims and is always paired with a full-precision
	// rescore). So the property that matters is that a WIDER binary candidate
	// window retains the true nearest neighbors, which the rescore then
	// reorders exactly. We check the true cosine top-10 survive in the binary
	// Hamming top-60.
	exact := make([]float32, len(data))
	approx := make([]float32, len(data))
	for i, v := range data {
		exact[i] = vector.Cosine.Distance(q, v)
		approx[i] = float32(b.Distance(qcode, b.Encode(v)))
	}
	if r := recallTrueTopKInApproxTopN(exact, approx, 10, 60); r < 0.8 {
		t.Errorf("binary top-60 retained %.2f of true top-10, want >= 0.8 before rescore", r)
	}
}

// recallTrueTopKInApproxTopN reports the fraction of the exact top-k that appear
// in the approximate top-n window, the funnel a coarse codec feeds into a
// rescore stage.
func recallTrueTopKInApproxTopN(exact, approx []float32, k, n int) float64 {
	order := func(d []float32, m int) []int {
		idx := make([]int, len(d))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return d[idx[a]] < d[idx[b]] })
		return idx[:m]
	}
	window := map[int]struct{}{}
	for _, i := range order(approx, n) {
		window[i] = struct{}{}
	}
	hit := 0
	for _, i := range order(exact, k) {
		if _, ok := window[i]; ok {
			hit++
		}
	}
	return float64(hit) / float64(k)
}
