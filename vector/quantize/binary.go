package quantize

import (
	"math/bits"

	"openindex/vector"
)

// Binary is 1-bit-per-dimension quantization (doc 06.4): each dimension becomes
// a single bit (sign relative to a per-dimension threshold) and similarity is
// Hamming distance via XOR plus popcount. It gives 32x compression and a large
// retrieval speedup, but it collapses on low-dimensional or near-zero vectors,
// so it is only ever a first pass: the Hamming sweep narrows to a candidate set
// that a full-precision (or int8) rescore then reorders. This codec produces
// the bits; the rescore lives in the retrieval tier (doc 07).
type Binary struct {
	threshold []float32 // per-dimension split point; a bit is set when v[d] > threshold[d]
}

// TrainBinary calibrates per-dimension thresholds as the sample mean of each
// dimension, the standard median-free split that balances set and unset bits
// when the data is roughly symmetric. With no sample the threshold is zero
// (sign quantization).
func TrainBinary(sample []vector.Vector, dim int) *Binary {
	th := make([]float32, dim)
	if len(sample) > 0 {
		for _, v := range sample {
			for d := range dim {
				th[d] += v[d]
			}
		}
		inv := float32(1) / float32(len(sample))
		for d := range dim {
			th[d] *= inv
		}
	}
	return &Binary{threshold: th}
}

// Dim reports the trained dimension.
func (b *Binary) Dim() int { return len(b.threshold) }

// CodeLen reports the encoded length in bytes (ceil(dim/8)).
func (b *Binary) CodeLen() int { return (len(b.threshold) + 7) / 8 }

// Encode packs v into a bitset: bit d is set when v[d] exceeds the calibrated
// threshold. Bits are packed LSB-first within each byte.
func (b *Binary) Encode(v vector.Vector) []byte {
	out := make([]byte, b.CodeLen())
	for d := range b.threshold {
		if v[d] > b.threshold[d] {
			out[d>>3] |= 1 << (uint(d) & 7)
		}
	}
	return out
}

// Distance is the Hamming distance between two codes: the population count of
// their XOR, summed word by word. This is the cheap first-pass score; a smaller
// value means more agreeing bits, hence more similar.
func (b *Binary) Distance(a, c []byte) int {
	var d int
	i := 0
	// Fold eight bytes at a time through a 64-bit popcount where possible.
	for ; i+8 <= len(a); i += 8 {
		x := le64(a[i:]) ^ le64(c[i:])
		d += bits.OnesCount64(x)
	}
	for ; i < len(a); i++ {
		d += bits.OnesCount8(a[i] ^ c[i])
	}
	return d
}

// le64 reads eight bytes as a little-endian uint64. The packing is LSB-first so
// the byte order here is irrelevant to correctness (both operands use it); it
// is only a way to popcount eight bytes per instruction.
func le64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}
