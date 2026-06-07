package serve

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"openindex"
)

func TestLRUGetSet(t *testing.T) {
	c := NewLRUCache(2)
	if _, ok := c.Get("missing"); ok {
		t.Fatal("empty cache should miss")
	}
	c.Set("a", []openindex.Result{res(1, 9)})
	got, ok := c.Get("a")
	if !ok || len(got) != 1 || got[0].Doc.Local != 1 {
		t.Fatalf("get after set failed: %v %v", got, ok)
	}
}

func TestLRUEviction(t *testing.T) {
	c := NewLRUCache(2)
	c.Set("a", []openindex.Result{res(1, 1)})
	c.Set("b", []openindex.Result{res(2, 2)})
	_, _ = c.Get("a")                         // a is now most-recently-used
	c.Set("c", []openindex.Result{res(3, 3)}) // evicts b, the least recent
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a was used recently and should survive")
	}
	if c.Len() != 2 {
		t.Fatalf("cache should hold its capacity, got %d", c.Len())
	}
}

func TestLRUUpdateInPlace(t *testing.T) {
	c := NewLRUCache(2)
	c.Set("a", []openindex.Result{res(1, 1)})
	c.Set("a", []openindex.Result{res(1, 5)})
	got, _ := c.Get("a")
	if got[0].Score != 5 {
		t.Fatalf("set should overwrite, got score %g", got[0].Score)
	}
	if c.Len() != 1 {
		t.Fatalf("overwrite should not grow the cache, got %d", c.Len())
	}
}

func TestLRUZeroCapacityStoresNothing(t *testing.T) {
	c := NewLRUCache(0)
	c.Set("a", []openindex.Result{res(1, 1)})
	if _, ok := c.Get("a"); ok {
		t.Fatal("a zero-capacity cache should store nothing")
	}
}

func TestLoaderCollapsesStampede(t *testing.T) {
	l := NewLoader(NewLRUCache(16))
	var loads int32
	const n = 50
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(n)
	got := make([][]openindex.Result, n)
	for i := range n {
		go func() {
			defer done.Done()
			start.Wait() // release all at once so they collide on the miss
			r, err := l.Get(context.Background(), "hot", func(context.Context) ([]openindex.Result, error) {
				atomic.AddInt32(&loads, 1)
				time.Sleep(40 * time.Millisecond) // hold the flight open
				return []openindex.Result{res(1, 9)}, nil
			})
			if err != nil {
				t.Errorf("get %d: %v", i, err)
			}
			got[i] = r
		}()
	}
	start.Done()
	done.Wait()
	if n := atomic.LoadInt32(&loads); n != 1 {
		t.Fatalf("stampede should collapse to one backend call, got %d", n)
	}
	for i := range got {
		if len(got[i]) != 1 || got[i][0].Doc.Local != 1 {
			t.Fatalf("caller %d got the wrong result: %v", i, got[i])
		}
	}
}

func TestLoaderServesFromCache(t *testing.T) {
	l := NewLoader(NewLRUCache(16))
	var loads int32
	load := func(context.Context) ([]openindex.Result, error) {
		atomic.AddInt32(&loads, 1)
		return []openindex.Result{res(1, 9)}, nil
	}
	for range 3 {
		if _, err := l.Get(context.Background(), "k", load); err != nil {
			t.Fatal(err)
		}
	}
	if atomic.LoadInt32(&loads) != 1 {
		t.Fatalf("after the first load the rest should hit the cache, got %d loads", loads)
	}
}

func TestLoaderDoesNotCacheErrors(t *testing.T) {
	l := NewLoader(NewLRUCache(16))
	boom := errors.New("backend down")
	if _, err := l.Get(context.Background(), "k", func(context.Context) ([]openindex.Result, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("first call should surface the error, got %v", err)
	}
	// A second call must re-run load, since the error was not cached.
	got, err := l.Get(context.Background(), "k", func(context.Context) ([]openindex.Result, error) {
		return []openindex.Result{res(2, 1)}, nil
	})
	if err != nil || len(got) != 1 || got[0].Doc.Local != 2 {
		t.Fatalf("second call should recompute: %v %v", got, err)
	}
}

func TestLoaderRespectsContext(t *testing.T) {
	l := NewLoader(NewLRUCache(16))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := l.Get(ctx, "k", func(c context.Context) ([]openindex.Result, error) {
		<-c.Done()
		return nil, c.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a caller whose deadline passes should get the deadline error, got %v", err)
	}
}
