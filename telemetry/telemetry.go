// Package telemetry is the observability leaf every role shares (impl spec
// 01.5, 02.3). It centralizes the RED-metric vocabulary (Rate, Errors,
// Duration) and structured logging so a fan-out tree is debuggable, and it
// imports nothing else in the module.
//
// The interfaces here are deliberately small and provider-agnostic. The full
// OpenTelemetry wiring - the gRPC stats handler, the SLO-aligned histogram
// buckets, the cardinality caps - is configured in impl spec 12; this package
// is the seam the rest of the code instruments against so that wiring can be
// swapped without touching call sites.
package telemetry

import (
	"context"
	"log/slog"
	"time"
)

// Meter records the three RED signals for one logical operation. Implementations
// must keep label cardinality low (service, method, status only); per-request
// identifiers belong on a span, never on a metric label (impl spec 01.5).
type Meter interface {
	// Observe records one completed operation: its wall-clock duration and
	// whether it errored. method must be a bounded, low-cardinality string.
	Observe(method string, dur time.Duration, err error)
}

// NopMeter discards every observation. It is the safe default so that a service
// with no configured MeterProvider still runs - but note that the real default
// OTel provider silently drops metrics, which looks like working
// instrumentation and is not (impl spec 01.5); a role must install a real Meter
// in production.
type NopMeter struct{}

// Observe implements Meter and does nothing.
func (NopMeter) Observe(string, time.Duration, error) {}

// Timer measures one operation from construction to Done. Typical use:
//
//	defer telemetry.Start(m, "Leaf.Search").Done(&err)
//
// where err is a named return; the deferred Done reads its final value.
type Timer struct {
	meter  Meter
	method string
	start  time.Time
	clock  func() time.Time
}

// Start begins timing method against meter. A nil meter is treated as a NopMeter
// so call sites never need a nil check.
func Start(m Meter, method string) *Timer {
	return startAt(m, method, time.Now)
}

// startAt is the injectable-clock form used by tests; production goes through
// Start with time.Now.
func startAt(m Meter, method string, clock func() time.Time) *Timer {
	if m == nil {
		m = NopMeter{}
	}
	return &Timer{meter: m, method: method, start: clock(), clock: clock}
}

// Done records the elapsed duration and the error. errp may be nil (no error)
// or point to an error value (nil-pointee also means no error), which lets it
// be driven by a deferred call against a named return.
func (t *Timer) Done(errp *error) {
	var err error
	if errp != nil {
		err = *errp
	}
	t.meter.Observe(t.method, t.clock().Sub(t.start), err)
}

// Logger returns the structured logger carried on ctx, or the package default
// if none is attached. Request-scoped fields (trace id, query id) are added to
// the context logger at the edge so every downstream line carries them.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithLogger returns a context carrying l, for the edge to seed request-scoped
// fields that propagate down the call tree.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

type loggerKey struct{}
