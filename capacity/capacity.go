// Package capacity turns the capacity and cost models of implementation doc 13
// into computable, tested functions. The doc states the storage, vector, and
// LLM-serving economics as formulas with reference figures; this package is
// those formulas as code, so a sizing decision is a function call a reviewer can
// check rather than a number in a slide. It implements the capacity concerns of
// architecture docs 04, 06, 09, and 12.
//
// The formulas are pure and side-effect-free. The reference figures from the
// spec (a 70B model near $0.45 per million tokens on an H100, a billion 128-dim
// float32 vectors at 512 GB, the int8-plus-disk saving near 85 percent) appear
// in the tests as the oracle the formulas are checked against, not as constants
// baked into the code, so a figure that moves with the hardware is a test edit,
// not a code edit.
package capacity

import "math"

// Component byte widths for a stored vector, the choice at the heart of the
// vector cost trade (doc 13.2): full precision in RAM is economically dead at
// scale, so the ANN sweep runs on a quantized copy and the exact rerank reads
// full precision off NVMe.
const (
	Float32 = 4 // full precision, the on-disk rerank copy
	Float16 = 2 // half precision
	Int8    = 1 // the quantized in-RAM ANN copy
)

// CostPer1MTokens returns the dollars per million tokens for an LLM deployment,
// from the GPU hourly price and the achieved decode throughput (doc 13.3):
//
//	cost_per_1M = price_per_hour / tokens_per_sec / 3600 * 1e6
//
// This is the figure that decides whether the answer engine survives against
// ten-blue-links, and it is built on throughput, not the hourly rate, because a
// faster GPU at a higher price can still win on cost per token. A non-positive
// throughput returns +Inf, the honest answer for a deployment that serves
// nothing.
func CostPer1MTokens(pricePerHour, tokensPerSec float64) float64 {
	if tokensPerSec <= 0 {
		return math.Inf(1)
	}
	return pricePerHour / tokensPerSec / 3600 * 1e6
}

// BlendedCost returns the mean cost per query across routing tiers, given the
// fraction of queries sent to each tier and the per-query cost of each (doc
// 13.3). The router is the survival mechanism: sending the bulk of queries to
// the no-LLM or small-model path holds most of the quality at a fraction of the
// cost, and this is the cost half of that trade. fractions and costs must be the
// same length; a mismatch or empty input returns 0.
func BlendedCost(fractions, costs []float64) float64 {
	if len(fractions) != len(costs) || len(fractions) == 0 {
		return 0
	}
	var total float64
	for i := range fractions {
		total += fractions[i] * costs[i]
	}
	return total
}

// VectorBytes returns the bytes to hold n vectors of dim dimensions at a given
// per-component width (doc 13.2). It is the raw arithmetic behind the tiering
// decision: a billion 128-dim float32 vectors is 512 GB, which is why the
// quantized copy holds RAM and the full-precision copy lives on disk. A
// non-positive count or dimension is 0.
func VectorBytes(n, dim, bytesPerComponent int) uint64 {
	if n <= 0 || dim <= 0 || bytesPerComponent <= 0 {
		return 0
	}
	return uint64(n) * uint64(dim) * uint64(bytesPerComponent)
}

// SavingFraction returns the fractional cost reduction of an alternative against
// a baseline, in [0,1] for a real saving (doc 13.2 reports a roughly 50-to-85
// percent saving for int8-plus-on-disk against all-in-RAM). A non-positive
// baseline returns 0, and an alternative dearer than the baseline returns a
// negative fraction rather than being clamped, so a regression is visible.
func SavingFraction(baseline, alternative float64) float64 {
	if baseline <= 0 {
		return 0
	}
	return (baseline - alternative) / baseline
}

// MemLimitBytes returns the recommended GOMEMLIMIT for a cgroup memory limit:
// the given fraction of the limit, leaving headroom for the off-heap and
// non-Go allocations the GC does not see (doc 13.4 recommends 85 to 90 percent).
// A fraction outside (0,1) is clamped into the recommended 0.85 to 0.90 band, so
// a misconfiguration cannot set the limit at or above the cgroup ceiling, which
// is the configuration that turns a memory spike into an OOM kill.
func MemLimitBytes(cgroupBytes uint64, fraction float64) uint64 {
	switch {
	case fraction < 0.85:
		fraction = 0.85
	case fraction > 0.90:
		fraction = 0.90
	}
	return uint64(float64(cgroupBytes) * fraction)
}
