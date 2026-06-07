package trace

import "testing"

func TestInjectExtractRoundTrip(t *testing.T) {
	sc := SpanContext{Trace: 42, Span: 7, Sampled: true}
	carrier := Inject(sc, nil)

	got, ok := Extract(carrier)
	if !ok {
		t.Fatal("a carrier written by Inject must extract")
	}
	if got != sc {
		t.Fatalf("round trip changed the context: got %+v, want %+v", got, sc)
	}
}

func TestSampledBitSurvivesPropagation(t *testing.T) {
	// The head sampling decision must travel across the boundary so the
	// downstream service honors it rather than re-sampling.
	for _, sampled := range []bool{true, false} {
		carrier := Inject(SpanContext{Trace: 1, Span: 1, Sampled: sampled}, nil)
		got, _ := Extract(carrier)
		if got.Sampled != sampled {
			t.Fatalf("sampled bit %v did not survive propagation", sampled)
		}
	}
}

func TestExtractRejectsMalformed(t *testing.T) {
	cases := []map[string]string{
		nil,
		{},
		{headerTrace: "not-a-number", headerSpan: "1"},
		{headerTrace: "0", headerSpan: "1"},
		{headerTrace: "1", headerSpan: "0"},
		{headerTrace: "1"}, // missing span
	}
	for i, c := range cases {
		if _, ok := Extract(c); ok {
			t.Fatalf("case %d: a malformed carrier must not extract", i)
		}
	}
}
