// Cross-process propagation of the span context (implementation doc 12.1). A
// gRPC client injects the current span context into request metadata and the
// server extracts it into a child span, which is what makes the leaf span a
// child of the root span across the network rather than the start of a fresh,
// disconnected trace. A carrier is the metadata-shaped string map both sides
// share; the header keys mirror the W3C trace-context fields so the format is
// recognizable.

package trace

import (
	"strconv"
)

// Carrier keys. They follow the W3C trace-context field names so a reader
// familiar with that standard recognizes them; the values are the decimal ids of
// this reference rather than the full hex traceparent.
const (
	headerTrace   = "x-openindex-trace"
	headerSpan    = "x-openindex-span"
	headerSampled = "x-openindex-sampled"
)

// Inject writes a span context into a carrier the way a client writes trace
// context into outgoing gRPC metadata. A nil carrier is created. The sampling
// bit is carried so the downstream service honors the edge's head decision
// instead of re-sampling, which is what keeps the trace whole.
func Inject(sc SpanContext, carrier map[string]string) map[string]string {
	if carrier == nil {
		carrier = map[string]string{}
	}
	carrier[headerTrace] = strconv.FormatUint(uint64(sc.Trace), 10)
	carrier[headerSpan] = strconv.FormatUint(uint64(sc.Span), 10)
	carrier[headerSampled] = strconv.FormatBool(sc.Sampled)
	return carrier
}

// Extract reads a span context out of a carrier the way a server reads it from
// incoming metadata. It reports false when the carrier holds no usable trace, so
// the server can start a fresh trace rather than stitch onto a malformed one. A
// trace whose id or span id does not parse, or is zero, is treated as absent.
func Extract(carrier map[string]string) (SpanContext, bool) {
	trace, err := strconv.ParseUint(carrier[headerTrace], 10, 64)
	if err != nil || trace == 0 {
		return SpanContext{}, false
	}
	span, err := strconv.ParseUint(carrier[headerSpan], 10, 64)
	if err != nil || span == 0 {
		return SpanContext{}, false
	}
	sampled, _ := strconv.ParseBool(carrier[headerSampled])
	return SpanContext{Trace: TraceID(trace), Span: SpanID(span), Sampled: sampled}, true
}
