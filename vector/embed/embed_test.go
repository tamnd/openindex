package embed

import (
	"context"
	"math"
	"testing"

	"openindex/vector"
)

func TestHashEmbedderDeterministicAndUnit(t *testing.T) {
	e := NewHashEmbedder(64)
	if e.Dim() != 64 {
		t.Fatalf("Dim=%d want 64", e.Dim())
	}
	ctx := context.Background()
	out1, err := e.Embed(ctx, []string{"hello world", "the quick brown fox"})
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := e.Embed(ctx, []string{"hello world", "the quick brown fox"})
	for i := range out1 {
		// Deterministic.
		for d := range out1[i] {
			if out1[i][d] != out2[i][d] {
				t.Fatalf("embedding %d not deterministic at dim %d", i, d)
			}
		}
		// Unit length (or zero for an empty token stream).
		var n float64
		for _, x := range out1[i] {
			n += float64(x) * float64(x)
		}
		if math.Abs(n-1) > 1e-5 {
			t.Errorf("embedding %d not unit length: norm^2=%.6f", i, n)
		}
	}
}

func TestHashEmbedderSimilarity(t *testing.T) {
	e := NewHashEmbedder(256)
	ctx := context.Background()
	out, _ := e.Embed(ctx, []string{
		"machine learning models",
		"machine learning models and systems",
		"completely unrelated banana",
	})
	near := Cosine(out[0], out[1])
	far := Cosine(out[0], out[2])
	if near <= far {
		t.Errorf("overlapping texts should be more similar: near=%.3f far=%.3f", near, far)
	}
}

func TestTokenize(t *testing.T) {
	got := tokenize("Hello, World!  Foo123bar")
	want := []string{"hello", "world", "foo123bar"}
	if len(got) != len(want) {
		t.Fatalf("tokens=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q want %q", i, got[i], want[i])
		}
	}
}

func TestCosineMismatch(t *testing.T) {
	if !math.IsNaN(Cosine(vector.Vector{1}, vector.Vector{1, 2})) {
		t.Error("mismatched dims should be NaN")
	}
}

// staticEmbedder is a trivial Embedder used to confirm the seam is satisfiable
// by an alternative implementation (the production gRPC client is another).
type staticEmbedder struct{ dim int }

func (s staticEmbedder) Dim() int { return s.dim }
func (s staticEmbedder) Embed(_ context.Context, texts []string) ([]vector.Vector, error) {
	out := make([]vector.Vector, len(texts))
	for i := range texts {
		out[i] = make(vector.Vector, s.dim)
	}
	return out, nil
}

func TestEmbedderInterface(t *testing.T) {
	var _ Embedder = NewHashEmbedder(8)
	var _ Embedder = staticEmbedder{dim: 8}
}
