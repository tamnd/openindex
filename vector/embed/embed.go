// Package embed is the embedding-serving boundary (architecture doc 06.5). The
// decision there is firm: embeddings come from an open dual-encoder
// (Qwen3-Embedding, BGE-M3, multilingual-E5) served as a separate process
// behind gRPC — never in the Go binary, never over cgo — because a transformer
// forward pass is GPU work that belongs in a Rust/Python inference server (TEI)
// with token-based dynamic batching, not bolted into the search fleet.
//
// So this package is a seam, not a model. Embedder is the interface the Go
// pipeline calls in batches; the production implementation is a gRPC client to
// the inference server, and HashEmbedder is the deterministic in-process
// reference the rest of the engine is tested against without a GPU.
package embed

import (
	"context"
	"errors"
	"hash/fnv"
	"math"

	"openindex/vector"
)

// Embedder turns text into dense vectors. Implementations must be safe for
// concurrent use and must return one vector per input in order, all of the same
// dimension. Batching is the caller's lever for throughput; the contract is
// per-call, so a caller may pass one string or ten thousand.
type Embedder interface {
	// Embed returns a vector for each text, in order. It honors ctx for
	// cancellation and deadlines (the gRPC client maps these to the RPC).
	Embed(ctx context.Context, texts []string) ([]vector.Vector, error)
	// Dim is the output dimension, fixed for the life of the embedder so a
	// segment can size its vector store ahead of the first call.
	Dim() int
}

// ErrClosed is returned by a closed embedder.
var ErrClosed = errors.New("embed: embedder closed")

// HashEmbedder is a deterministic, dependency-free reference Embedder: it maps
// text to a unit vector by hashing tokens into dimensions (the hashing-trick /
// random-projection sketch). It is NOT semantic — it shares the real model's
// interface, output dimension, and L2-normalized output, which is all the rest
// of the engine needs to be exercised offline. The same text always yields the
// same vector, so index builds and tests are reproducible.
type HashEmbedder struct {
	dim int
}

// NewHashEmbedder returns a reference embedder of the given dimension.
func NewHashEmbedder(dim int) *HashEmbedder { return &HashEmbedder{dim: dim} }

// Dim reports the output dimension.
func (e *HashEmbedder) Dim() int { return e.dim }

// Embed produces one L2-normalized vector per text. A token sets a sign on the
// dimension it hashes to (the signed hashing trick), then the vector is
// normalized so cosine and inner-product behave as the real encoder's outputs
// do.
func (e *HashEmbedder) Embed(_ context.Context, texts []string) ([]vector.Vector, error) {
	out := make([]vector.Vector, len(texts))
	for i, t := range texts {
		out[i] = e.embedOne(t)
	}
	return out, nil
}

func (e *HashEmbedder) embedOne(text string) vector.Vector {
	v := make(vector.Vector, e.dim)
	for _, tok := range tokenize(text) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum64()
		d := int(sum % uint64(e.dim))
		// Top bit of the hash chooses the sign, decorrelating collisions.
		if sum&(1<<63) != 0 {
			v[d] += 1
		} else {
			v[d] -= 1
		}
	}
	return vector.Normalize(v)
}

// tokenize splits text into lowercase ASCII word tokens. It is intentionally
// crude: the reference embedder only needs a stable token stream, and the real
// model does its own subword tokenization server-side.
func tokenize(text string) []string {
	var toks []string
	start := -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		isWord := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		switch {
		case isWord && start < 0:
			start = i
		case !isWord && start >= 0:
			toks = append(toks, lower(text[start:i]))
			start = -1
		}
	}
	if start >= 0 {
		toks = append(toks, lower(text[start:]))
	}
	return toks
}

// lower lowercases an ASCII token without allocating through strings.ToLower's
// Unicode path.
func lower(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if b == nil {
				b = []byte(s)
			}
			b[i] = c + ('a' - 'A')
		}
	}
	if b == nil {
		return s
	}
	return string(b)
}

// Cosine is a small convenience for callers comparing two embeddings, hiding the
// metric choice behind the package that owns the embedding contract. It assumes
// unit-length inputs (Embed returns them), so it is just the dot product mapped
// to a similarity in [-1,1].
func Cosine(a, b vector.Vector) float64 {
	if len(a) != len(b) {
		return math.NaN()
	}
	var d float64
	for i := range a {
		d += float64(a[i]) * float64(b[i])
	}
	return d
}
