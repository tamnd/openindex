// Package quantize holds the vector compression codecs (architecture doc 06.4),
// in increasing compression: scalar int8, product quantization, and binary.
// Full-precision vectors in RAM do not pay for themselves at web scale, so the
// engine keeps a compressed representation in memory for the ANN candidate
// sweep and the full-precision vectors on NVMe for an exact rescore of the
// small candidate set (the tiered pattern of doc 06.4). Each codec is a
// Quantizer: trained once on a sample, then it encodes vectors to bytes and
// answers approximate distances from the codes.
package quantize

import (
	"math"

	"openindex/vector"
)

// Scalar is per-dimension int8 quantization (doc 06.4): each dimension is
// linearly mapped from its calibrated [min,max] range onto [-127,127]. It gives
// 4x compression at minimal recall loss, which makes it the conservative
// default for the re-rank tier. Calibration is per-dimension, not global,
// because embedding dimensions have very different dynamic ranges.
type Scalar struct {
	min   []float32 // per-dimension calibrated minimum
	scale []float32 // per-dimension (max-min)/254, the quantization step
}

// TrainScalar calibrates a Scalar over a sample, taking each dimension's
// observed min and max as its range. A constant dimension gets a zero scale and
// encodes to 0, which dequantizes back to its constant value.
func TrainScalar(sample []vector.Vector) *Scalar {
	if len(sample) == 0 {
		return &Scalar{}
	}
	dim := len(sample[0])
	lo := make([]float32, dim)
	hi := make([]float32, dim)
	for d := range dim {
		lo[d], hi[d] = float32(math.Inf(1)), float32(math.Inf(-1))
	}
	for _, v := range sample {
		for d, x := range v {
			if x < lo[d] {
				lo[d] = x
			}
			if x > hi[d] {
				hi[d] = x
			}
		}
	}
	scale := make([]float32, dim)
	for d := range dim {
		if hi[d] > lo[d] {
			scale[d] = (hi[d] - lo[d]) / 254
		}
	}
	return &Scalar{min: lo, scale: scale}
}

// Dim reports the trained dimension.
func (s *Scalar) Dim() int { return len(s.min) }

// Encode maps v to one signed byte per dimension. Values outside the calibrated
// range saturate at the endpoints rather than wrapping.
func (s *Scalar) Encode(v vector.Vector) []int8 {
	out := make([]int8, len(v))
	for d, x := range v {
		if s.scale[d] == 0 {
			continue
		}
		q := int32(math.Round(float64((x - s.min[d]) / s.scale[d])))
		q -= 127 // recenter [0,254] onto [-127,127]
		if q > 127 {
			q = 127
		} else if q < -127 {
			q = -127
		}
		out[d] = int8(q)
	}
	return out
}

// Decode reconstructs an approximate vector from its codes, the operation the
// exact rescore tier does not need but that makes the codec testable and lets a
// caller materialize a dequantized vector when convenient.
func (s *Scalar) Decode(code []int8) vector.Vector {
	out := make(vector.Vector, len(code))
	for d, c := range code {
		out[d] = s.min[d] + (float32(c)+127)*s.scale[d]
	}
	return out
}

// Distance approximates the L2-squared distance between a full-precision query
// and an encoded database vector by dequantizing the codes on the fly. The
// query stays full precision (the asymmetric form), which keeps the dominant
// error on the database side where the compression lives.
func (s *Scalar) Distance(query vector.Vector, code []int8) float32 {
	var sum float32
	for d, c := range code {
		dq := s.min[d] + (float32(c)+127)*s.scale[d]
		diff := query[d] - dq
		sum += diff * diff
	}
	return sum
}
