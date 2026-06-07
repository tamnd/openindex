package capacity

import (
	"math"
	"testing"
)

func TestCostPer1MTokensFormula(t *testing.T) {
	// One dollar an hour at a thousand tokens a second:
	// 1 / 1000 / 3600 * 1e6 = 0.2778 dollars per million tokens.
	got := CostPer1MTokens(1.0, 1000)
	if math.Abs(got-0.27778) > 1e-4 {
		t.Fatalf("cost = %v, want about 0.27778", got)
	}
	// A faster, dearer GPU can still win on cost per token: twice the price but
	// three times the throughput is cheaper per token.
	slow := CostPer1MTokens(1.0, 1000)
	fast := CostPer1MTokens(2.0, 3000)
	if fast >= slow {
		t.Fatalf("the faster GPU should be cheaper per token: fast %v, slow %v", fast, slow)
	}
}

func TestCostPer1MTokensNoThroughput(t *testing.T) {
	if got := CostPer1MTokens(5.0, 0); !math.IsInf(got, 1) {
		t.Fatalf("zero throughput must cost +Inf, got %v", got)
	}
}

func TestBlendedCostRouterSaving(t *testing.T) {
	// Send 85 percent of queries to a cheap path and 15 percent to the
	// expensive LLM path. The blended cost is far below the all-LLM cost, which
	// is the router survival mechanism in numbers.
	cheap, dear := 0.01, 1.00
	blended := BlendedCost([]float64{0.85, 0.15}, []float64{cheap, dear})
	if blended >= dear {
		t.Fatalf("blended cost %v should be well below the all-LLM cost %v", blended, dear)
	}
	want := 0.85*cheap + 0.15*dear
	if math.Abs(blended-want) > 1e-9 {
		t.Fatalf("blended = %v, want %v", blended, want)
	}
	// A length mismatch is a misuse, not a silent partial sum.
	if got := BlendedCost([]float64{1.0}, []float64{0.1, 0.2}); got != 0 {
		t.Fatalf("mismatched lengths must return 0, got %v", got)
	}
}

func TestVectorBytesReferenceFigures(t *testing.T) {
	const billion = 1_000_000_000
	// A billion 128-dim float32 vectors is about 512 GB, the doc 13.2 figure
	// that rules out holding raw vectors in RAM at scale.
	wantGB := float64(VectorBytes(billion, 128, Float32)) / 1e9
	if math.Abs(wantGB-512) > 1 {
		t.Fatalf("a billion 128-dim float32 vectors = %.0f GB, want about 512", wantGB)
	}
	// The int8 copy is a quarter of the float32 copy.
	if VectorBytes(billion, 128, Int8)*4 != VectorBytes(billion, 128, Float32) {
		t.Fatal("the int8 copy must be a quarter the size of the float32 copy")
	}
	if VectorBytes(0, 128, Float32) != 0 || VectorBytes(billion, 0, Float32) != 0 {
		t.Fatal("a zero count or dimension must be 0 bytes")
	}
}

func TestSavingFraction(t *testing.T) {
	// The doc 13.2 example: about $45k/month all-in-RAM versus about $6.5k/month
	// int8-plus-disk is roughly an 85 percent saving.
	got := SavingFraction(45000, 6500)
	if got < 0.84 || got > 0.86 {
		t.Fatalf("saving = %.3f, want about 0.85", got)
	}
	// A dearer alternative shows as a negative saving, not a clamped zero.
	if SavingFraction(100, 120) >= 0 {
		t.Fatal("an alternative dearer than the baseline must read as a negative saving")
	}
	if SavingFraction(0, 10) != 0 {
		t.Fatal("a zero baseline must return 0")
	}
}

func TestMemLimitBytesBandAndClamp(t *testing.T) {
	var limit uint64 = 64 << 30 // 64 GiB cgroup limit
	got := MemLimitBytes(limit, 0.875)
	if want := uint64(float64(limit) * 0.875); got != want {
		t.Fatalf("memlimit = %d, want %d", got, want)
	}
	// A fraction at or above the ceiling is clamped down to the band, so the
	// limit never sits at the cgroup ceiling where a spike becomes an OOM kill.
	high := MemLimitBytes(limit, 1.5)
	if high >= limit {
		t.Fatalf("memlimit %d must stay below the cgroup limit %d", high, limit)
	}
	if high != uint64(float64(limit)*0.90) {
		t.Fatal("an over-range fraction must clamp to the 0.90 top of the band")
	}
	low := MemLimitBytes(limit, 0.10)
	if low != uint64(float64(limit)*0.85) {
		t.Fatal("an under-range fraction must clamp to the 0.85 floor of the band")
	}
}
