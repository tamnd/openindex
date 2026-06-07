package serve

import (
	"container/list"
	"context"
	"sync"

	"golang.org/x/sync/singleflight"

	"openindex"
)

// Cache is the result-cache seam. The production in-process cache is Ristretto:
// TinyLFU admission with sampled-LFU eviction, cost-based and concurrent, which
// is best-in-class on hit ratio but deliberately drops some Set calls (a new
// item may not be admitted), acceptable for a cache (doc 08.3). LRUCache below
// is the in-process reference behind this seam.
//
// A cache key is the normalized query plus the snapshot id; the value is the
// final ranked page. The front-end result cache serves a page with zero backend
// work but only on an exact-query hit, so its ceiling is low (most unique
// queries are singletons, doc 08.3); the posting-list cache carries more of the
// load and lives in the leaf.
type Cache interface {
	Get(key string) ([]openindex.Result, bool)
	Set(key string, val []openindex.Result)
}

// LRUCache is a fixed-capacity, least-recently-used result cache, safe for
// concurrent use. It is the reference: a plain replacement policy that stands
// in for Ristretto's admission policy so the serving path is testable without
// the dependency. Unlike Ristretto it admits every Set, so a test sees a
// deterministic hit pattern.
type LRUCache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	items    map[string]*list.Element
}

type entry struct {
	key string
	val []openindex.Result
}

// NewLRUCache returns a cache holding at most capacity entries. A capacity <= 0
// makes a cache that stores nothing, so every lookup misses.
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
	}
}

// Get returns the cached page for key and moves it to most-recently-used.
func (c *LRUCache) Get(key string) ([]openindex.Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*entry).val, true
}

// Set stores val under key, evicting the least-recently-used entry if the cache
// is over capacity.
func (c *LRUCache) Set(key string, val []openindex.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacity <= 0 {
		return
	}
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).val = val
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&entry{key: key, val: val})
	c.items[key] = el
	if c.ll.Len() > c.capacity {
		c.evict()
	}
}

// Len reports the number of cached entries.
func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *LRUCache) evict() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry).key)
}

// Loader fronts a Cache with single-flight stampede protection: when many
// queries miss the same hot key at once, only one backend call runs and the
// rest wait on its result (doc 08.3, doc 01.3). Without this, a popular query
// expiring from the cache lets every concurrent request hit the backend at
// once, which is exactly when the backend can least afford it.
type Loader struct {
	cache Cache
	group singleflight.Group
}

// NewLoader wraps a cache.
func NewLoader(cache Cache) *Loader {
	return &Loader{cache: cache}
}

// Get returns the cached page for key, or runs load to compute it, stores it,
// and returns it. Concurrent Gets for the same key while load is running share
// the one in-flight call rather than each running load. The wait respects the
// context: a caller whose deadline passes returns ctx.Err() even while the
// shared load is still running for the others. A load error is not cached.
func (l *Loader) Get(ctx context.Context, key string, load func(context.Context) ([]openindex.Result, error)) ([]openindex.Result, error) {
	if v, ok := l.cache.Get(key); ok {
		return v, nil
	}
	ch := l.group.DoChan(key, func() (any, error) {
		v, err := load(ctx)
		if err != nil {
			return nil, err
		}
		l.cache.Set(key, v)
		return v, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]openindex.Result), nil
	}
}
