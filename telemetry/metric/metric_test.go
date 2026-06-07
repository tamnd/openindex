package metric

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func find(snaps []Snapshot, method, status string) (Snapshot, bool) {
	for _, s := range snaps {
		if s.Method == method && s.Status == status {
			return s, true
		}
	}
	return Snapshot{}, false
}

func TestObserveCountsAndBuckets(t *testing.T) {
	r := New([]string{"Leaf.Search"}, []float64{10, 100})
	// Three durations: one in the first bucket (<=10), one in the second
	// (<=100), one past every bound (the overflow slot).
	r.Observe("Leaf.Search", ms(5), nil)
	r.Observe("Leaf.Search", ms(50), nil)
	r.Observe("Leaf.Search", ms(500), nil)

	s, ok := find(r.Snapshot(), "Leaf.Search", "OK")
	if !ok {
		t.Fatal("expected a Leaf.Search/OK series")
	}
	if s.Count != 3 {
		t.Fatalf("count = %d, want 3", s.Count)
	}
	if s.Errors != 0 {
		t.Fatalf("errors = %d, want 0", s.Errors)
	}
	want := []uint64{1, 1, 1} // [<=10], [<=100], [overflow]
	for i, w := range want {
		if s.BucketCounts[i] != w {
			t.Fatalf("bucket %d = %d, want %d", i, s.BucketCounts[i], w)
		}
	}
}

func TestErrorsAndStatusSplit(t *testing.T) {
	r := New([]string{"Root.Query"}, nil)
	r.Observe("Root.Query", ms(10), nil)
	r.Observe("Root.Query", ms(10), errors.New("boom"))
	r.Observe("Root.Query", ms(10), context.DeadlineExceeded)
	r.Observe("Root.Query", ms(10), context.Canceled)

	if got := r.ErrorRate("Root.Query"); got != 0.75 {
		t.Fatalf("error rate = %v, want 0.75 (3 of 4 errored)", got)
	}
	for _, status := range []string{"OK", "error", "deadline_exceeded", "canceled"} {
		if _, ok := find(r.Snapshot(), "Root.Query", status); !ok {
			t.Fatalf("expected a Root.Query/%s series", status)
		}
	}
}

func TestUnknownMethodCollapsesToOther(t *testing.T) {
	r := New([]string{"Leaf.Search"}, nil)
	r.Observe("Some.Unlisted.Probe", ms(5), nil)

	if _, ok := find(r.Snapshot(), "_OTHER", "OK"); !ok {
		t.Fatal("an unlisted method must record under _OTHER, not its own series")
	}
	if _, ok := find(r.Snapshot(), "Some.Unlisted.Probe", "OK"); ok {
		t.Fatal("an unlisted method must not create its own series")
	}
}

func TestCardinalityCapFoldsToOther(t *testing.T) {
	// Allow many methods but cap the series, then drive more distinct methods
	// than the cap. The series count must stay bounded and the overflow must
	// land under _OTHER rather than unbounding memory.
	methods := make([]string, 0, 50)
	for i := range 50 {
		methods = append(methods, fmt.Sprintf("M%02d", i))
	}
	r := New(methods, nil, WithMaxSeries(8))
	for i := range 50 {
		r.Observe(fmt.Sprintf("M%02d", i), ms(5), nil)
	}

	snaps := r.Snapshot()
	if len(snaps) > 8 {
		t.Fatalf("series count = %d, want <= 8 (the cap)", len(snaps))
	}
	other, ok := find(snaps, "_OTHER", "OK")
	if !ok {
		t.Fatal("the methods past the cap must fold into _OTHER")
	}
	var total uint64
	for _, s := range snaps {
		total += s.Count
	}
	if total != 50 {
		t.Fatalf("total observations = %d, want 50 (none dropped)", total)
	}
	if other.Count == 0 {
		t.Fatal("_OTHER must carry the folded observations")
	}
}

func TestQuantileInterpolates(t *testing.T) {
	// One hundred observations evenly spread across [0,100] ms with fine bounds.
	// The P50 should land near 50 ms and the P99 near the top.
	bounds := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	r := New([]string{"Leaf.Search"}, bounds)
	for i := range 100 {
		r.Observe("Leaf.Search", ms(i), nil)
	}
	s, _ := find(r.Snapshot(), "Leaf.Search", "OK")

	p50 := s.Quantile(0.50)
	if p50 < 45 || p50 > 55 {
		t.Fatalf("P50 = %v ms, want roughly 50", p50)
	}
	p99 := s.Quantile(0.99)
	if p99 < 90 {
		t.Fatalf("P99 = %v ms, want at least 90", p99)
	}
	if mean := s.Mean(); mean < 45 || mean > 55 {
		t.Fatalf("mean = %v ms, want roughly 50", mean)
	}
}

func TestEmptySeriesQuantileIsZero(t *testing.T) {
	var s Snapshot
	if got := s.Quantile(0.99); got != 0 {
		t.Fatalf("quantile of an empty series = %v, want 0", got)
	}
	if got := s.Mean(); got != 0 {
		t.Fatalf("mean of an empty series = %v, want 0", got)
	}
}

func TestDefaultBucketsUsedWhenEmpty(t *testing.T) {
	r := New([]string{"Leaf.Search"}, nil)
	r.Observe("Leaf.Search", ms(1), nil)
	s, _ := find(r.Snapshot(), "Leaf.Search", "OK")
	if len(s.Bounds) != len(DefaultBucketsMillis) {
		t.Fatalf("bounds length = %d, want the %d default buckets", len(s.Bounds), len(DefaultBucketsMillis))
	}
	if len(s.BucketCounts) != len(DefaultBucketsMillis)+1 {
		t.Fatalf("bucket counts length = %d, want bounds+1 for the overflow slot", len(s.BucketCounts))
	}
}
