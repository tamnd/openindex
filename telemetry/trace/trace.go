// Package trace stitches one query's fan-out into a single trace
// (implementation doc 12.1). A query touches the frontend, the root, several
// aggregators, thousands of leaves, the snippet servers, and sometimes the
// answer engine, so a span at a leaf has to be a child of the span at the root
// rather than an orphan. This package is the in-process reference for that: a
// span carries a context, a child reads its parent from the request context and
// inherits the trace, and a carrier injects and extracts the context across a
// process boundary the way a gRPC client and server move it through metadata.
//
// Two decisions from doc 12.1 are built in. Sampling is head-based: one decision
// is made at the edge when the trace starts and propagated unchanged, so a
// sampled trace is complete across the whole tree instead of half-sampled at the
// leaves. And the recorder keeps a trace tail-style even when head sampling
// dropped it, if any span errored or ran past a slow threshold, because those
// are exactly the traces worth keeping for the tail.
package trace

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// TraceID identifies one trace across every span in the fan-out. SpanID
// identifies one span within it. They are integers here for a self-contained,
// deterministic reference; the production binding uses the 16- and 8-byte random
// ids of the OpenTelemetry wire format.
type (
	TraceID uint64
	SpanID  uint64
)

// SpanContext is the propagated identity of a span: the trace it belongs to, its
// own id, and the head sampling decision. The sampling bit travels with the
// context so a downstream service honors the edge's decision rather than making
// its own, which is what keeps a sampled trace whole.
type SpanContext struct {
	Trace   TraceID
	Span    SpanID
	Sampled bool
}

// Valid reports whether a span context names a real trace, the test Extract uses
// to tell a propagated context from a zero value.
func (sc SpanContext) Valid() bool { return sc.Trace != 0 && sc.Span != 0 }

// Sampler decides whether a new trace is sampled, given its id. The decision is
// made once, at the edge, in StartTrace.
type Sampler interface {
	Sample(TraceID) bool
}

// AlwaysSample samples every trace; NeverSample samples none. They are the two
// ends a test or a development build uses.
type (
	alwaysSample struct{}
	neverSample  struct{}
)

func (alwaysSample) Sample(TraceID) bool { return true }
func (neverSample) Sample(TraceID) bool  { return false }

// AlwaysSample returns a sampler that keeps every trace.
func AlwaysSample() Sampler { return alwaysSample{} }

// NeverSample returns a sampler that keeps no trace at the head; the recorder's
// tail rule still keeps errored and slow ones.
func NeverSample() Sampler { return neverSample{} }

// RatioSampler keeps a deterministic, pseudo-uniform fraction of traces. The
// decision is a hash of the trace id rather than a coin flip, so it is stable
// (the same trace samples the same way) and needs no source of randomness, which
// also keeps the package replayable.
type RatioSampler struct {
	Ratio float64
}

// Sample keeps the trace when the hashed id falls in the kept fraction. A ratio
// at or below zero keeps nothing; at or above one keeps everything.
func (r RatioSampler) Sample(id TraceID) bool {
	switch {
	case r.Ratio <= 0:
		return false
	case r.Ratio >= 1:
		return true
	}
	return float64(mix64(uint64(id)))/float64(^uint64(0)) < r.Ratio
}

// mix64 is the SplitMix64 finalizer, a cheap full-avalanche hash so adjacent
// trace ids do not land in the same side of the sampling cut.
func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// Tracer starts spans and routes finished ones to a recorder. One tracer is
// shared across a node's goroutines; it is safe for concurrent use.
type Tracer struct {
	sampler  Sampler
	recorder *Recorder
	slow     time.Duration
	clock    func() time.Time
	seq      atomic.Uint64
}

// NewTracer returns a tracer that samples with s and records to rec. slow is the
// tail-keep threshold: a span at least this long is recorded even on an unsampled
// trace; a non-positive slow disables the duration rule, leaving only the error
// rule. A nil recorder is allowed and drops every span.
func NewTracer(s Sampler, rec *Recorder, slow time.Duration) *Tracer {
	if s == nil {
		s = NeverSample()
	}
	return &Tracer{sampler: s, recorder: rec, slow: slow, clock: time.Now}
}

// StartTrace begins a new root span at the edge and makes the one head sampling
// decision for the whole trace. It returns a context carrying the new span and
// the span itself; downstream StartSpan calls read that context to stitch their
// spans underneath. Use this once per query, at the frontend.
func (t *Tracer) StartTrace(ctx context.Context, name string) (context.Context, *Span) {
	id := TraceID(t.seq.Add(1))
	sc := SpanContext{Trace: id, Span: SpanID(t.seq.Add(1)), Sampled: t.sampler.Sample(id)}
	return t.begin(ctx, name, sc, 0)
}

// StartSpan begins a child span under the span on ctx, inheriting its trace and
// its sampling decision so the child belongs to the same trace as its parent. If
// ctx carries no span (a call that skipped the edge), it falls back to starting
// a fresh trace so a span is never silently dropped.
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	parent, ok := SpanContextFromContext(ctx)
	if !ok || !parent.Valid() {
		return t.StartTrace(ctx, name)
	}
	child := SpanContext{Trace: parent.Trace, Span: SpanID(t.seq.Add(1)), Sampled: parent.Sampled}
	return t.begin(ctx, name, child, parent.Span)
}

// begin records the start of a span and returns it on a derived context.
func (t *Tracer) begin(ctx context.Context, name string, sc SpanContext, parent SpanID) (context.Context, *Span) {
	s := &Span{
		tracer: t,
		name:   name,
		ctx:    sc,
		parent: parent,
		start:  t.clock(),
	}
	return ContextWithSpanContext(ctx, sc), s
}

// Span is one node of the trace tree, in flight until End. Set attributes with
// SetAttr; they ride along to the recorded span.
type Span struct {
	tracer *Tracer
	name   string
	ctx    SpanContext
	parent SpanID
	start  time.Time
	attrs  map[string]string
}

// Context returns the span's propagation context, the value a client injects
// into a downstream request.
func (s *Span) Context() SpanContext { return s.ctx }

// SetAttr attaches a key-value attribute to the span. Per-request identifiers
// (query, document, user) belong here, on the span, not on a metric label (doc
// 12.2).
func (s *Span) SetAttr(key, value string) {
	if s.attrs == nil {
		s.attrs = map[string]string{}
	}
	s.attrs[key] = value
}

// End closes the span and offers it to the recorder. errp may be nil or point to
// a named-return error, so End can be deferred the same way the metric Timer is.
// Whether the span is actually kept is the recorder's tail decision.
func (s *Span) End(errp *error) {
	var err error
	if errp != nil {
		err = *errp
	}
	if s.tracer.recorder == nil {
		return
	}
	dur := s.tracer.clock().Sub(s.start)
	s.tracer.recorder.offer(SpanData{
		Trace:    s.ctx.Trace,
		Span:     s.ctx.Span,
		Parent:   s.parent,
		Name:     s.name,
		Duration: dur,
		Errored:  err != nil,
		Sampled:  s.ctx.Sampled,
		Attrs:    s.attrs,
	}, s.tracer.slow)
}

// SpanData is a finished span as the recorder keeps it.
type SpanData struct {
	Trace    TraceID
	Span     SpanID
	Parent   SpanID
	Name     string
	Duration time.Duration
	Errored  bool
	Sampled  bool
	Attrs    map[string]string
}

// Recorder is the in-process stand-in for the collector: it keeps the finished
// spans that survive the keep rule. The rule is head-or-tail: keep a span if its
// trace was head-sampled, or, regardless of sampling, if it errored or ran past
// the tracer's slow threshold. That is the tail-based keep of doc 12.1, which
// rescues the slow and broken traces head sampling would have thrown away.
type Recorder struct {
	mu    sync.Mutex
	spans []SpanData
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// offer applies the keep rule and stores the span if it survives.
func (r *Recorder) offer(s SpanData, slow time.Duration) {
	keep := s.Sampled || s.Errored || (slow > 0 && s.Duration >= slow)
	if !keep {
		return
	}
	r.mu.Lock()
	r.spans = append(r.spans, s)
	r.mu.Unlock()
}

// Spans returns a copy of every kept span, in the order they finished.
func (r *Recorder) Spans() []SpanData {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SpanData(nil), r.spans...)
}

// Trace returns the kept spans of one trace, the tree a debugger reconstructs
// for a single query.
func (r *Recorder) Trace(id TraceID) []SpanData {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SpanData
	for _, s := range r.spans {
		if s.Trace == id {
			out = append(out, s)
		}
	}
	return out
}

// Reset drops every kept span, for reuse between test cases.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.spans = nil
	r.mu.Unlock()
}

type spanContextKey struct{}

// ContextWithSpanContext returns a context carrying sc, so a downstream
// StartSpan finds its parent. This is the in-process half of propagation; Inject
// and Extract carry it across a process boundary.
func ContextWithSpanContext(ctx context.Context, sc SpanContext) context.Context {
	return context.WithValue(ctx, spanContextKey{}, sc)
}

// SpanContextFromContext returns the span context carried on ctx and whether one
// was present.
func SpanContextFromContext(ctx context.Context) (SpanContext, bool) {
	sc, ok := ctx.Value(spanContextKey{}).(SpanContext)
	return sc, ok
}
