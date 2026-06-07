// Package frontier is the crawl scheduler: it decides what to fetch next and
// when, holding the politeness and prioritization policy of crawler doc 04.2.
// It is built against two seams - a SeenSet (has this URL been enqueued
// before?) and the host-politeness clock - so the in-memory reference
// implementations here can be swapped for the disk-backed DRUM store and a
// distributed scheduler without touching the queue logic.
package frontier

import "sync"

// SeenSet records which URL fingerprints the frontier has already admitted, so
// the same page is not enqueued twice. Production uses DRUM (Disk Repository
// with Update Management, 04.2): URL fingerprints are batched and merged against
// a disk store in sorted runs, turning random dedup lookups into sequential IO.
// The interface is the seam; this is the bucket the queue is tested against.
type SeenSet interface {
	// Seen reports whether fp has been admitted before and records it as seen.
	// It returns true if the fingerprint was already present.
	Seen(fp uint64) bool
	// Len reports how many distinct fingerprints are held.
	Len() int
}

// MemSeenSet is an in-memory SeenSet for a single shard's working set and for
// tests. It is safe for concurrent use.
type MemSeenSet struct {
	mu  sync.Mutex
	set map[uint64]struct{}
}

// NewMemSeenSet returns an empty set.
func NewMemSeenSet() *MemSeenSet {
	return &MemSeenSet{set: make(map[uint64]struct{})}
}

// Seen records fp and reports whether it was already present.
func (s *MemSeenSet) Seen(fp uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.set[fp]; ok {
		return true
	}
	s.set[fp] = struct{}{}
	return false
}

// Len reports the number of distinct fingerprints held.
func (s *MemSeenSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.set)
}
