package trace

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClock returns a clock function reading a mutable time, so a test can make
// a span take a controlled duration without sleeping.
func fakeClock(now *time.Time) func() time.Time {
	return func() time.Time { return *now }
}

func TestChildStitchesUnderParent(t *testing.T) {
	rec := NewRecorder()
	tr := NewTracer(AlwaysSample(), rec, 0)

	ctx, root := tr.StartTrace(context.Background(), "Root.Query")
	_, child := tr.StartSpan(ctx, "Leaf.Search")
	child.End(nil)
	root.End(nil)

	if child.Context().Trace != root.Context().Trace {
		t.Fatal("a child span must share its parent's trace id")
	}
	if child.Context().Span == root.Context().Span {
		t.Fatal("a child span must have its own span id")
	}
	spans := rec.Trace(root.Context().Trace)
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans for the trace, want 2", len(spans))
	}
	var leaf SpanData
	for _, s := range spans {
		if s.Name == "Leaf.Search" {
			leaf = s
		}
	}
	if leaf.Parent != root.Context().Span {
		t.Fatalf("leaf parent = %d, want the root span %d", leaf.Parent, root.Context().Span)
	}
}

func TestHeadSamplingPropagatesToChild(t *testing.T) {
	tr := NewTracer(AlwaysSample(), NewRecorder(), 0)
	ctx, root := tr.StartTrace(context.Background(), "Root.Query")
	_, child := tr.StartSpan(ctx, "Leaf.Search")
	if !root.Context().Sampled || !child.Context().Sampled {
		t.Fatal("an always-sampled trace must mark both root and child sampled")
	}

	tr2 := NewTracer(NeverSample(), NewRecorder(), 0)
	ctx2, root2 := tr2.StartTrace(context.Background(), "Root.Query")
	_, child2 := tr2.StartSpan(ctx2, "Leaf.Search")
	if root2.Context().Sampled || child2.Context().Sampled {
		t.Fatal("a never-sampled trace must mark both root and child unsampled")
	}
}

func TestTailKeepsSlowAndErrored(t *testing.T) {
	rec := NewRecorder()
	now := time.Unix(0, 0)
	tr := NewTracer(NeverSample(), rec, 50*time.Millisecond)
	tr.clock = fakeClock(&now)

	// A fast, clean, unsampled span: dropped.
	_, fast := tr.StartTrace(context.Background(), "fast")
	now = now.Add(5 * time.Millisecond)
	fast.End(nil)

	// A slow, clean, unsampled span: kept by the tail duration rule.
	now = time.Unix(0, 0)
	_, slow := tr.StartTrace(context.Background(), "slow")
	now = now.Add(80 * time.Millisecond)
	slow.End(nil)

	// A fast, errored, unsampled span: kept by the tail error rule.
	now = time.Unix(0, 0)
	_, bad := tr.StartTrace(context.Background(), "bad")
	now = now.Add(1 * time.Millisecond)
	err := errors.New("crash")
	bad.End(&err)

	names := map[string]bool{}
	for _, s := range rec.Spans() {
		names[s.Name] = true
	}
	if names["fast"] {
		t.Fatal("a fast clean unsampled span must be dropped")
	}
	if !names["slow"] {
		t.Fatal("a slow span must be kept even when unsampled")
	}
	if !names["bad"] {
		t.Fatal("an errored span must be kept even when unsampled")
	}
}

func TestStartSpanWithoutParentStartsTrace(t *testing.T) {
	tr := NewTracer(AlwaysSample(), NewRecorder(), 0)
	// No span on the context: StartSpan must not drop the span, it starts a
	// fresh trace so the work is still recorded.
	_, s := tr.StartSpan(context.Background(), "orphan")
	if !s.Context().Valid() {
		t.Fatal("a parentless StartSpan must still produce a valid root span")
	}
}

func TestRatioSamplerDeterministicAndBounded(t *testing.T) {
	s := RatioSampler{Ratio: 0.5}
	// Deterministic: the same id always decides the same way.
	for id := TraceID(1); id < 20; id++ {
		first := s.Sample(id)
		if first != s.Sample(id) {
			t.Fatalf("ratio sampler is not deterministic for id %d", id)
		}
	}
	// The extremes are absolute.
	if (RatioSampler{Ratio: 0}).Sample(7) {
		t.Fatal("ratio 0 must sample nothing")
	}
	if !(RatioSampler{Ratio: 1}).Sample(7) {
		t.Fatal("ratio 1 must sample everything")
	}
	// Roughly half of a large id range is kept.
	var kept int
	const n = 10000
	for id := TraceID(1); id <= n; id++ {
		if s.Sample(id) {
			kept++
		}
	}
	if kept < n*4/10 || kept > n*6/10 {
		t.Fatalf("ratio 0.5 kept %d of %d, want roughly half", kept, n)
	}
}

func TestSetAttrRidesAlong(t *testing.T) {
	rec := NewRecorder()
	tr := NewTracer(AlwaysSample(), rec, 0)
	_, s := tr.StartTrace(context.Background(), "Root.Query")
	s.SetAttr("query.id", "q-42")
	s.End(nil)
	got := rec.Spans()
	if len(got) != 1 || got[0].Attrs["query.id"] != "q-42" {
		t.Fatalf("span attribute did not reach the recorder: %+v", got)
	}
}
