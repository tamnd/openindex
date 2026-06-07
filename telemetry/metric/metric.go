// Package metric is the RED-metric backend behind the telemetry.Meter seam
// (implementation doc 12.2). The root telemetry package defines the Observe
// vocabulary and a no-op default; this package is the real recorder a role
// installs in production: per (method, status) request and error counts and an
// SLO-aligned latency histogram, with the cardinality controls that keep the
// metrics affordable across a fleet of thousands of leaves.
//
// Two rules from doc 12.2 are enforced here rather than left to discipline.
// First, the histogram buckets are explicit and chosen to bracket the roughly
// 200 ms serving budget (DefaultBucketsMillis), not the SDK defaults, whose
// bounds are not stable and do not land usefully around the budget. Second,
// label cardinality is bounded: a method outside the allowlist collapses to
// "_OTHER" per the OpenTelemetry semantic conventions, the status is a small
// fixed set, and the total number of tracked series is capped so a cardinality
// blowout cannot turn into an outage of its own.
package metric

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// DefaultBucketsMillis is the SLO-aligned latency histogram, in milliseconds.
// The bounds bracket the roughly 200 ms tail budget (doc 08, doc 12.2): fine
// resolution below the budget where the P99 lives, coarser above it where the
// only question is how far a request overshot.
var DefaultBucketsMillis = []float64{2, 5, 10, 25, 50, 100, 250, 500, 1000}

// DefaultMaxSeries is the cap on distinct (method, status) series one recorder
// tracks, matching the Go metric SDK's default cardinality limit. Past the cap
// new series fold into the overflow method "_OTHER" so an unforeseen method
// explosion degrades resolution rather than unbounding memory.
const DefaultMaxSeries = 2000

// otherMethod is the bucket an unrecognized or overflow method collapses to,
// the value the OTel semantic conventions reserve for rpc.method (doc 12.2).
const otherMethod = "_OTHER"

// overflowReserve is the number of series slots held back for the "_OTHER"
// overflow method, one per status. Holding them back keeps maxSeries a true
// ceiling: real-method series are admitted only while the map has room for the
// overflow series the cap will eventually force, so the map never grows past
// maxSeries no matter how many distinct methods arrive.
const overflowReserve = 4

// seriesKey is the full label set of one series. These are the only labels a
// metric carries: low-cardinality and bounded. Per-request identifiers (query,
// document, user) belong on a span attribute, never here (doc 12.2).
type seriesKey struct {
	method string
	status string
}

// hist is the accumulated state of one series: a request count, an error count,
// the sum of durations for the mean, and the per-bucket counts. counts has one
// more slot than there are bounds: the final slot is the overflow bucket for
// durations past the largest bound.
type hist struct {
	count  uint64
	errors uint64
	sum    float64
	counts []uint64
}

// RED is a Rate-Errors-Duration recorder implementing telemetry.Meter. It is
// safe for concurrent use by every in-flight request on a node.
type RED struct {
	mu        sync.Mutex
	bounds    []float64
	allowed   map[string]struct{}
	maxSeries int
	series    map[seriesKey]*hist
}

// Option configures a RED recorder at construction.
type Option func(*RED)

// WithMaxSeries overrides the default series cap. A value of zero or less is
// ignored so a misconfiguration cannot disable the cardinality guard.
func WithMaxSeries(n int) Option {
	return func(r *RED) {
		if n > 0 {
			r.maxSeries = n
		}
	}
}

// New returns a RED recorder. methods is the allowlist of recognized method
// names; an Observe for any other method records under "_OTHER" so an
// instrumentation bug or a probe hitting an unknown route cannot fan the metric
// out by method. bounds is the histogram boundary set in milliseconds; an empty
// bounds uses DefaultBucketsMillis. The bounds are sorted ascending so the
// quantile estimate can walk them in order.
func New(methods []string, bounds []float64, opts ...Option) *RED {
	b := append([]float64(nil), bounds...)
	if len(b) == 0 {
		b = append(b, DefaultBucketsMillis...)
	}
	sort.Float64s(b)
	allowed := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		allowed[m] = struct{}{}
	}
	r := &RED{
		bounds:    b,
		allowed:   allowed,
		maxSeries: DefaultMaxSeries,
		series:    map[seriesKey]*hist{},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Observe records one completed operation. It implements telemetry.Meter: the
// duration lands in the right histogram bucket, the request count rises, and a
// non-nil error raises the error count for the series. The method is normalized
// to the allowlist and the series count is capped before anything is recorded,
// so the cardinality guard runs on the hot path, not in a later sweep.
func (r *RED) Observe(method string, dur time.Duration, err error) {
	status := classify(err)

	r.mu.Lock()
	defer r.mu.Unlock()

	key := r.keyFor(method, status)
	h := r.series[key]
	if h == nil {
		h = &hist{counts: make([]uint64, len(r.bounds)+1)}
		r.series[key] = h
	}

	ms := float64(dur) / float64(time.Millisecond)
	h.count++
	h.sum += ms
	if status != "OK" {
		h.errors++
	}
	h.counts[r.bucketOf(ms)]++
}

// keyFor normalizes a method to the allowlist and applies the series cap. A
// method not on the allowlist becomes "_OTHER". If recording a new series would
// exceed the cap, the method also folds into "_OTHER", so the number of distinct
// series stays bounded under any input. The caller holds r.mu.
func (r *RED) keyFor(method, status string) seriesKey {
	if _, ok := r.allowed[method]; !ok {
		method = otherMethod
	}
	key := seriesKey{method: method, status: status}
	if _, exists := r.series[key]; exists {
		return key
	}
	// Admit a new real-method series only while the map has room for it and the
	// overflow series the cap reserves. Past that point a new method folds into
	// "_OTHER", whose own series fit in the reserved slots, so the total never
	// exceeds maxSeries.
	if len(r.series) >= r.maxSeries-overflowReserve {
		return seriesKey{method: otherMethod, status: status}
	}
	return key
}

// bucketOf returns the index of the histogram bucket a millisecond duration
// falls in: the first bound it does not exceed, or the overflow slot past the
// last bound. The caller holds r.mu.
func (r *RED) bucketOf(ms float64) int {
	for i, b := range r.bounds {
		if ms <= b {
			return i
		}
	}
	return len(r.bounds)
}

// classify maps an error to a bounded status label. The set is small on purpose
// (doc 12.2): a deadline and a cancellation are the two failure modes worth
// distinguishing on the fan-out tail, everything else is "error", and nil is
// "OK". A real gRPC binding would use the status code, which is itself a fixed
// low-cardinality enum.
func classify(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

// Snapshot is the recorded state of one series at a point in time, the shape a
// test asserts against and an exporter reads. Bounds is shared (read-only)
// across snapshots from the same recorder; BucketCounts has one more slot than
// Bounds for the overflow bucket.
type Snapshot struct {
	Method       string
	Status       string
	Count        uint64
	Errors       uint64
	SumMillis    float64
	Bounds       []float64
	BucketCounts []uint64
}

// Snapshot returns the current state of every series, ordered by method then
// status so the output is deterministic for golden comparisons and stable logs.
func (r *RED) Snapshot() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Snapshot, 0, len(r.series))
	for key, h := range r.series {
		out = append(out, Snapshot{
			Method:       key.method,
			Status:       key.status,
			Count:        h.count,
			Errors:       h.errors,
			SumMillis:    h.sum,
			Bounds:       r.bounds,
			BucketCounts: append([]uint64(nil), h.counts...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		return out[i].Status < out[j].Status
	})
	return out
}

// ErrorRate is the fraction of requests for a method that errored, across all
// of its statuses, the Errors signal of RED. An unobserved method is 0. The
// method is normalized to the allowlist so it matches what Observe recorded.
func (r *RED) ErrorRate(method string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.allowed[method]; !ok {
		method = otherMethod
	}
	var total, errs uint64
	for key, h := range r.series {
		if key.method == method {
			total += h.count
			errs += h.errors
		}
	}
	if total == 0 {
		return 0
	}
	return float64(errs) / float64(total)
}

// Quantile estimates the q-quantile (0..1) of a series' latency in milliseconds
// from its histogram, by linear interpolation within the bucket the quantile
// falls in, the same construction Prometheus uses. An empty series is 0, and a
// quantile that lands in the overflow bucket returns the largest bound since the
// histogram has no upper edge there. It is the per-shard tail diagnostic of doc
// 12.2: the slowest few percent of leaves are half the root tail.
func (s Snapshot) Quantile(q float64) float64 {
	if s.Count == 0 {
		return 0
	}
	target := q * float64(s.Count)
	var cum uint64
	for i, c := range s.BucketCounts {
		before := cum
		cum += c
		if float64(cum) < target || c == 0 {
			continue
		}
		lower := 0.0
		if i > 0 {
			lower = s.Bounds[i-1]
		}
		if i >= len(s.Bounds) {
			return lower
		}
		upper := s.Bounds[i]
		frac := (target - float64(before)) / float64(c)
		return lower + frac*(upper-lower)
	}
	return s.Bounds[len(s.Bounds)-1]
}

// Mean is the arithmetic mean latency of a series in milliseconds, or 0 when the
// series is empty.
func (s Snapshot) Mean() float64 {
	if s.Count == 0 {
		return 0
	}
	return s.SumMillis / float64(s.Count)
}
