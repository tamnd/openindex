package quantize

import (
	"errors"
	"math"
	"math/rand/v2"

	"openindex/vector"
)

// PQ is Product Quantization (doc 06.4): the vector is split into m contiguous
// subvectors, a k-means codebook of 256 centroids is trained per subspace, and
// each subvector is stored as the one-byte index of its nearest centroid. With
// m subspaces that is m bytes per vector, up to ~64x compression, and it is the
// in-RAM candidate-sweep representation at scale.
//
// The win is the asymmetric distance (ADC) query path: the query stays full
// precision, an m-by-256 table of squared partial distances is precomputed once
// per query, and each database vector's distance is then m table lookups summed
// (O(m)), memory-bandwidth bound because the table fits in L1/L2. Build with
// NewADC once per query, then call Distance per candidate.
type PQ struct {
	m     int         // subspaces
	sub   int         // dimension per subspace
	dim   int         // m*sub
	books [][]float32 // m codebooks, each 256*sub floats, row-major
}

// ksize is the per-subspace codebook size, fixed at 256 so a code fits one byte.
const ksize = 256

// TrainPQ trains a product quantizer with m subspaces over a sample using
// Lloyd's k-means per subspace. The vector dimension must be divisible by m.
// iters bounds the k-means refinement; seed makes training deterministic.
func TrainPQ(sample []vector.Vector, m, iters int, seed uint64) (*PQ, error) {
	if len(sample) == 0 {
		return nil, errors.New("quantize: empty training sample")
	}
	dim := len(sample[0])
	if m <= 0 || dim%m != 0 {
		return nil, errors.New("quantize: dimension not divisible by subspace count")
	}
	sub := dim / m
	rng := rand.New(rand.NewPCG(seed, seed^0xd1b54a32d192ed03))
	pq := &PQ{m: m, sub: sub, dim: dim, books: make([][]float32, m)}
	for s := range m {
		pq.books[s] = trainSubspace(sample, s*sub, sub, iters, rng)
	}
	return pq, nil
}

// trainSubspace runs k-means on one subspace slice [off:off+sub] of the sample.
func trainSubspace(sample []vector.Vector, off, sub, iters int, rng *rand.Rand) []float32 {
	// Fewer points than centroids: each point becomes its own centroid.
	k := min(ksize, len(sample))
	book := make([]float32, ksize*sub)
	// Initialize centroids from distinct random sample rows.
	perm := rng.Perm(len(sample))
	for c := range k {
		copy(book[c*sub:(c+1)*sub], sample[perm[c]][off:off+sub])
	}
	// Fill any unused centroid rows (k < 256) by repeating the last, so every
	// code value dequantizes to something valid.
	for c := k; c < ksize; c++ {
		copy(book[c*sub:(c+1)*sub], book[(k-1)*sub:k*sub])
	}

	assign := make([]int, len(sample))
	for range iters {
		// Assignment step: nearest centroid per sample subvector.
		changed := false
		for i, v := range sample {
			seg := v[off : off+sub]
			best, bestD := 0, float32(math.Inf(1))
			for c := range k {
				if d := l2subsq(seg, book[c*sub:(c+1)*sub]); d < bestD {
					best, bestD = c, d
				}
			}
			if assign[i] != best {
				assign[i], changed = best, true
			}
		}
		// Update step: each centroid becomes the mean of its members.
		sums := make([]float32, ksize*sub)
		counts := make([]int, ksize)
		for i, v := range sample {
			c := assign[i]
			counts[c]++
			seg := v[off : off+sub]
			for d := range sub {
				sums[c*sub+d] += seg[d]
			}
		}
		for c := range k {
			if counts[c] == 0 {
				continue // keep an emptied centroid where it was
			}
			inv := 1 / float32(counts[c])
			for d := range sub {
				book[c*sub+d] = sums[c*sub+d] * inv
			}
		}
		if !changed {
			break
		}
	}
	return book
}

// Dim reports the trained vector dimension.
func (pq *PQ) Dim() int { return pq.dim }

// M reports the number of subspaces, which is the code length in bytes.
func (pq *PQ) M() int { return pq.m }

// Encode quantizes v to one centroid index per subspace.
func (pq *PQ) Encode(v vector.Vector) []byte {
	code := make([]byte, pq.m)
	for s := range pq.m {
		off := s * pq.sub
		seg := v[off : off+pq.sub]
		best, bestD := 0, float32(math.Inf(1))
		for c := range ksize {
			if d := l2subsq(seg, pq.books[s][c*pq.sub:(c+1)*pq.sub]); d < bestD {
				best, bestD = c, d
			}
		}
		code[s] = byte(best)
	}
	return code
}

// Decode reconstructs the approximate vector by concatenating the chosen
// centroids, used for an exact rescore or for testing reconstruction error.
func (pq *PQ) Decode(code []byte) vector.Vector {
	out := make(vector.Vector, pq.dim)
	for s, ci := range code {
		off := s * pq.sub
		copy(out[off:off+pq.sub], pq.books[s][int(ci)*pq.sub:(int(ci)+1)*pq.sub])
	}
	return out
}

// ADC is a per-query asymmetric-distance table: table[s*256+c] is the squared
// distance from the query's s-th subvector to centroid c of subspace s. Reuse
// it across all candidates of one query.
type ADC struct {
	pq    *PQ
	table []float32
}

// NewADC precomputes the partial-distance table for query q. This is the
// once-per-query cost that makes each subsequent Distance O(m) lookups.
func (pq *PQ) NewADC(q vector.Vector) *ADC {
	table := make([]float32, pq.m*ksize)
	for s := range pq.m {
		off := s * pq.sub
		seg := q[off : off+pq.sub]
		for c := range ksize {
			table[s*ksize+c] = l2subsq(seg, pq.books[s][c*pq.sub:(c+1)*pq.sub])
		}
	}
	return &ADC{pq: pq, table: table}
}

// Distance sums the precomputed partial distances for an encoded vector: the
// O(m) table-lookup core of the ADC sweep.
func (a *ADC) Distance(code []byte) float32 {
	var sum float32
	for s, ci := range code {
		sum += a.table[s*ksize+int(ci)]
	}
	return sum
}

// l2subsq is the squared Euclidean distance between two equal-length subvector
// slices, the k-means and ADC inner kernel.
func l2subsq(a, b []float32) float32 {
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}
