package serve

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"openindex"
)

// fakeLeaf is an in-process Leaf for exercising the fan-out without a network.
type fakeLeaf struct {
	results []openindex.Result
	err     error
	delay   time.Duration // return after this long, or when the context ends
	block   bool          // block until the context ends, then return its error
	calls   *int32        // optional in-flight counter
}

func (f fakeLeaf) Search(ctx context.Context, _ Request) (Response, error) {
	if f.calls != nil {
		atomic.AddInt32(f.calls, 1)
	}
	if f.err != nil {
		return Response{}, f.err
	}
	if f.block {
		<-ctx.Done()
		return Response{}, ctx.Err()
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	return Response{Results: f.results}, nil
}

func res(local openindex.DocID, score openindex.Score) openindex.Result {
	return openindex.Result{Doc: openindex.GlobalDocID{Local: local}, Score: score}
}

func leafReturning(rs ...openindex.Result) fakeLeaf { return fakeLeaf{results: rs} }

func TestScatterGathersAll(t *testing.T) {
	leaves := []Leaf{
		leafReturning(res(1, 9)),
		leafReturning(res(2, 8)),
		leafReturning(res(3, 7)),
	}
	g := Scatter(context.Background(), leaves, Request{K: 10}, FanoutConfig{})
	if g.Responded() != 3 {
		t.Fatalf("all shards should respond, got %d", g.Responded())
	}
	if len(g.OKResponses()) != 3 {
		t.Fatalf("OKResponses should drop no gaps, got %d", len(g.OKResponses()))
	}
}

func TestScatterToleratesLeafError(t *testing.T) {
	leaves := []Leaf{
		leafReturning(res(1, 9)),
		fakeLeaf{err: errors.New("leaf down")},
		leafReturning(res(3, 7)),
	}
	g := Scatter(context.Background(), leaves, Request{K: 10}, FanoutConfig{})
	if g.Responded() != 2 {
		t.Fatalf("a leaf error should degrade to a missing shard, got %d responded", g.Responded())
	}
	if g.OK[1] {
		t.Fatal("the failed shard should be marked not OK")
	}
	// The query did not fail: the surviving shards produced results.
	if len(g.OKResponses()) != 2 {
		t.Fatalf("survivors should still merge, got %d", len(g.OKResponses()))
	}
}

func TestScatterPerShardTimeout(t *testing.T) {
	leaves := []Leaf{
		leafReturning(res(1, 9)),
		fakeLeaf{block: true}, // never returns until cancelled
	}
	start := time.Now()
	g := Scatter(context.Background(), leaves, Request{K: 10}, FanoutConfig{PerShardTimeout: 30 * time.Millisecond})
	elapsed := time.Since(start)
	if g.Responded() != 1 || g.OK[1] {
		t.Fatalf("the slow shard should miss its sub-deadline, got responded=%d ok[1]=%v", g.Responded(), g.OK[1])
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("scatter should return near the sub-deadline, took %v", elapsed)
	}
}

func TestScatterRespectsParentDeadline(t *testing.T) {
	leaves := []Leaf{fakeLeaf{block: true}, fakeLeaf{block: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	g := Scatter(ctx, leaves, Request{K: 10}, FanoutConfig{})
	if g.Responded() != 0 {
		t.Fatalf("a cancelled parent should leave no responders, got %d", g.Responded())
	}
}

func TestScatterBoundsConcurrency(t *testing.T) {
	const n = 20
	var inflight, peak int32
	leaves := make([]Leaf, n)
	for i := range leaves {
		leaves[i] = countingLeaf{inflight: &inflight, peak: &peak}
	}
	Scatter(context.Background(), leaves, Request{K: 1}, FanoutConfig{MaxConcurrent: 4, PerShardTimeout: time.Second})
	if got := atomic.LoadInt32(&peak); got > 4 {
		t.Fatalf("MaxConcurrent=4 but observed %d leaves in flight at once", got)
	}
}

// countingLeaf tracks the peak number of concurrent Search calls.
type countingLeaf struct {
	inflight *int32
	peak     *int32
}

func (l countingLeaf) Search(_ context.Context, _ Request) (Response, error) {
	cur := atomic.AddInt32(l.inflight, 1)
	for {
		p := atomic.LoadInt32(l.peak)
		if cur <= p || atomic.CompareAndSwapInt32(l.peak, p, cur) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	atomic.AddInt32(l.inflight, -1)
	return Response{}, nil
}
