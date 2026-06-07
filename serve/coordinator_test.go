package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"openindex"
)

func TestCoordinatorMergesChildren(t *testing.T) {
	children := []Leaf{
		leafReturning(res(1, 9), res(4, 3)),
		leafReturning(res(2, 8)),
		leafReturning(res(3, 7)),
	}
	c := NewCoordinator(children, FanoutConfig{PerShardTimeout: time.Second}, 1)
	got, err := c.Search(context.Background(), Request{K: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []openindex.DocID{1, 2, 3}
	if len(got) != 3 {
		t.Fatalf("want top 3, got %d", len(got))
	}
	for i, w := range want {
		if got[i].Doc.Local != w {
			t.Fatalf("rank %d: got %d want %d", i, got[i].Doc.Local, w)
		}
	}
}

func TestCoordinatorGoodEnoughCutoff(t *testing.T) {
	// Four children, one hangs. A 0.75 floor needs three, which arrive, so the
	// slow shard is dropped and the query still succeeds.
	children := []Leaf{
		leafReturning(res(1, 9)),
		leafReturning(res(2, 8)),
		leafReturning(res(3, 7)),
		fakeLeaf{block: true},
	}
	c := NewCoordinator(children, FanoutConfig{PerShardTimeout: 30 * time.Millisecond}, 0.75)
	got, err := c.Search(context.Background(), Request{K: 10})
	if err != nil {
		t.Fatalf("three of four should clear a 0.75 floor: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("should merge the three survivors, got %d", len(got))
	}
}

func TestCoordinatorInsufficientShards(t *testing.T) {
	// Three of four children hang; a strict 1.0 floor cannot be met.
	children := []Leaf{
		leafReturning(res(1, 9)),
		fakeLeaf{block: true},
		fakeLeaf{block: true},
		fakeLeaf{block: true},
	}
	c := NewCoordinator(children, FanoutConfig{PerShardTimeout: 30 * time.Millisecond}, 1)
	_, err := c.Search(context.Background(), Request{K: 10})
	if !errors.Is(err, ErrInsufficientShards) {
		t.Fatalf("a degraded fan-out should not be served as complete, got %v", err)
	}
}

func TestCoordinatorContextCancelBeatsInsufficient(t *testing.T) {
	children := []Leaf{fakeLeaf{block: true}, fakeLeaf{block: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c(children).Search(ctx, Request{K: 10})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled query should report the context error, got %v", err)
	}
}

func TestCoordinatorNoChildren(t *testing.T) {
	got, err := NewCoordinator(nil, FanoutConfig{}, 1).Search(context.Background(), Request{K: 5})
	if err != nil || got != nil {
		t.Fatalf("no children should return nil, nil; got %v %v", got, err)
	}
}

func TestNewCoordinatorClampsFraction(t *testing.T) {
	if got := NewCoordinator(nil, FanoutConfig{}, 0).minResponded; got != 1 {
		t.Fatalf("fraction 0 should clamp to 1, got %g", got)
	}
	if got := NewCoordinator(nil, FanoutConfig{}, 2).minResponded; got != 1 {
		t.Fatalf("fraction >1 should clamp to 1, got %g", got)
	}
}

// c builds a strict coordinator over the children for the brevity of one test.
func c(children []Leaf) *Coordinator {
	return NewCoordinator(children, FanoutConfig{PerShardTimeout: 30 * time.Millisecond}, 1)
}
