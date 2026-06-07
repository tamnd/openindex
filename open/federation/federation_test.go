package federation

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"openindex"
	"openindex/open"
)

// stubLeaf is a partition leaf that returns canned results, optionally after
// honoring its context so a deadline test is deterministic.
type stubLeaf struct {
	results []openindex.Result
	err     error
	block   bool // block until the context is cancelled, then return its error
}

func (s stubLeaf) Search(ctx context.Context, _ string, _ int) ([]openindex.Result, error) {
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.results, s.err
}

func result(url string, score float32) openindex.Result {
	return openindex.Result{URL: url, Score: openindex.Score(score)}
}

// registerOperator generates a key, adds it to the registry, and gives the
// operator a passing reputation, the common setup for a trusted partition.
func registerOperator(reg *open.Registry, g *Gate, op OperatorID, rep float64) {
	pub, _, _ := ed25519.GenerateKey(nil)
	reg.Add(op, pub)
	g.SetReputation(op, rep)
}

func TestGateExcludesUnknownAndLowReputation(t *testing.T) {
	reg := open.NewRegistry()
	g := NewGate(reg, 0.5)
	registerOperator(reg, g, "trusted", 0.9)
	registerOperator(reg, g, "shady", 0.1)

	if !g.Trusted(Partition{Operator: "trusted"}) {
		t.Fatal("a known operator above the floor should be trusted")
	}
	if g.Trusted(Partition{Operator: "shady"}) {
		t.Fatal("a known operator below the reputation floor should be excluded")
	}
	if g.Trusted(Partition{Operator: "stranger"}) {
		t.Fatal("an operator with no registered key should be excluded")
	}
}

func TestSearchMergesTrustedPartitions(t *testing.T) {
	reg := open.NewRegistry()
	g := NewGate(reg, 0.5)
	registerOperator(reg, g, "a", 0.9)
	registerOperator(reg, g, "b", 0.9)
	registerOperator(reg, g, "evil", 0.0) // below the floor, must not contribute

	parts := []Partition{
		{Operator: "a", Leaf: stubLeaf{results: []openindex.Result{result("a1", 0.9), result("a2", 0.4)}}},
		{Operator: "b", Leaf: stubLeaf{results: []openindex.Result{result("b1", 0.7)}}},
		{Operator: "evil", Leaf: stubLeaf{results: []openindex.Result{result("spam", 1.0)}}},
	}
	f := &Federator{Gate: g, Deadline: time.Second, K: 3}
	got := f.Search(context.Background(), "q", parts)

	if len(got) != 3 {
		t.Fatalf("expected 3 merged results, got %d: %+v", len(got), got)
	}
	// Best-first, and the untrusted top-scored spam result must be absent.
	if got[0].URL != "a1" || got[1].URL != "b1" || got[2].URL != "a2" {
		t.Fatalf("merge order wrong: %+v", got)
	}
	for _, r := range got {
		if r.URL == "spam" {
			t.Fatal("an untrusted partition's result leaked into the merge")
		}
	}
}

func TestSearchDropsErroringAndSlowPartitions(t *testing.T) {
	reg := open.NewRegistry()
	g := NewGate(reg, 0.5)
	registerOperator(reg, g, "fast", 0.9)
	registerOperator(reg, g, "broken", 0.9)
	registerOperator(reg, g, "slow", 0.9)

	parts := []Partition{
		{Operator: "fast", Leaf: stubLeaf{results: []openindex.Result{result("ok", 0.8)}}},
		{Operator: "broken", Leaf: stubLeaf{err: errors.New("partition down")}},
		{Operator: "slow", Leaf: stubLeaf{block: true}}, // never answers within the deadline
	}
	f := &Federator{Gate: g, Deadline: 20 * time.Millisecond, K: 5}
	got := f.Search(context.Background(), "q", parts)

	if len(got) != 1 || got[0].URL != "ok" {
		t.Fatalf("only the fast partition should survive, got %+v", got)
	}
}

func TestSearchTruncatesToK(t *testing.T) {
	reg := open.NewRegistry()
	g := NewGate(reg, 0.5)
	registerOperator(reg, g, "a", 0.9)
	parts := []Partition{{Operator: "a", Leaf: stubLeaf{results: []openindex.Result{
		result("1", 0.9), result("2", 0.8), result("3", 0.7), result("4", 0.6),
	}}}}
	f := &Federator{Gate: g, Deadline: time.Second, K: 2}
	if got := f.Search(context.Background(), "q", parts); len(got) != 2 {
		t.Fatalf("K=2 should cap the merge at 2, got %d", len(got))
	}
}

func TestVerifyRebuild(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	reg := open.NewRegistry()
	reg.Add("op", pub)

	corpus := []byte("the published index bytes")
	a := open.Artifact{Kind: open.KindCIFF, Snapshot: "snap-1", Content: open.Address(corpus), Operator: "op"}
	a.Sig = open.Sign(a, priv)

	// An honest rebuild reproduces the bytes and verifies.
	if err := VerifyRebuild(reg, a, corpus); err != nil {
		t.Fatalf("an honest rebuild should verify: %v", err)
	}
	// A rebuild that does not reproduce is rejected even with a valid signature.
	if err := VerifyRebuild(reg, a, []byte("tampered bytes")); !errors.Is(err, ErrRebuildMismatch) {
		t.Fatalf("a mismatched rebuild should be rejected, got %v", err)
	}
	// An unknown operator is rejected before the rebuild is even compared.
	bad := a
	bad.Operator = "stranger"
	if err := VerifyRebuild(reg, bad, corpus); !errors.Is(err, open.ErrUnknownOperator) {
		t.Fatalf("an unknown operator should be rejected, got %v", err)
	}
}
