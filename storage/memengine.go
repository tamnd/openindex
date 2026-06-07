package storage

import (
	"bytes"
	"sort"
	"sync"
)

// MemEngine is an ordered in-memory Engine: the reference implementation the
// WebTable and link-graph code are tested against. It keeps entries in one
// lexicographically sorted slice with binary-search lookups, which is the
// access shape a real LSM presents (point get, ordered range scan) without the
// on-disk machinery. It is not the production store - that is Pebble/Badger/bbolt
// behind this same interface (03.1) - but it is correct, snapshot-isolated, and
// concurrency-safe, which is what the tests need.
type MemEngine struct {
	mu      sync.RWMutex
	entries []entry // sorted by key
}

type entry struct {
	key, value []byte
}

// NewMemEngine returns an empty in-memory engine.
func NewMemEngine() *MemEngine { return &MemEngine{} }

// search returns the index of key and whether it was found, assuming the caller
// holds at least the read lock.
func (m *MemEngine) search(key []byte) (int, bool) {
	i := sort.Search(len(m.entries), func(i int) bool {
		return bytes.Compare(m.entries[i].key, key) >= 0
	})
	if i < len(m.entries) && bytes.Equal(m.entries[i].key, key) {
		return i, true
	}
	return i, false
}

// Get implements Engine.
func (m *MemEngine) Get(key []byte) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if i, ok := m.search(key); ok {
		return clone(m.entries[i].value), true, nil
	}
	return nil, false, nil
}

// set inserts or overwrites under the write lock.
func (m *MemEngine) set(key, value []byte) {
	i, ok := m.search(key)
	if ok {
		m.entries[i].value = clone(value)
		return
	}
	m.entries = append(m.entries, entry{})
	copy(m.entries[i+1:], m.entries[i:])
	m.entries[i] = entry{key: clone(key), value: clone(value)}
}

func (m *MemEngine) del(key []byte) {
	if i, ok := m.search(key); ok {
		m.entries = append(m.entries[:i], m.entries[i+1:]...)
	}
}

// Set implements Engine.
func (m *MemEngine) Set(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.set(key, value)
	return nil
}

// Delete implements Engine.
func (m *MemEngine) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.del(key)
	return nil
}

// Apply implements Engine: the whole batch lands under one lock, so a reader
// never observes a partial commit.
func (m *MemEngine) Apply(b *Batch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range b.ops {
		if o.del {
			m.del(o.key)
		} else {
			m.set(o.key, o.value)
		}
	}
	return nil
}

// Scan implements Engine over the live data.
func (m *MemEngine) Scan(start, end []byte) Iterator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanLocked(start, end)
}

// scanLocked snapshots the matching range into a slice so the iterator does not
// hold the lock while the caller walks it.
func (m *MemEngine) scanLocked(start, end []byte) Iterator {
	lo := 0
	if start != nil {
		lo, _ = m.search(start)
	}
	hi := len(m.entries)
	if end != nil {
		hi, _ = m.search(end)
	}
	out := make([]entry, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, entry{key: clone(m.entries[i].key), value: clone(m.entries[i].value)})
	}
	return &sliceIter{entries: out, pos: -1}
}

// Snapshot implements Engine by copying the current entries. A copy is the
// honest semantics - later writes cannot be seen - and is fine at test scale; a
// production engine uses its native MVCC snapshot here.
func (m *MemEngine) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]entry, len(m.entries))
	for i, e := range m.entries {
		cp[i] = entry{key: clone(e.key), value: clone(e.value)}
	}
	return &memSnapshot{entries: cp}
}

// Close implements Engine.
func (m *MemEngine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = nil
	return nil
}

type memSnapshot struct{ entries []entry }

func (s *memSnapshot) Get(key []byte) ([]byte, bool, error) {
	i := sort.Search(len(s.entries), func(i int) bool {
		return bytes.Compare(s.entries[i].key, key) >= 0
	})
	if i < len(s.entries) && bytes.Equal(s.entries[i].key, key) {
		return clone(s.entries[i].value), true, nil
	}
	return nil, false, nil
}

func (s *memSnapshot) Scan(start, end []byte) Iterator {
	lo := 0
	if start != nil {
		lo = sort.Search(len(s.entries), func(i int) bool { return bytes.Compare(s.entries[i].key, start) >= 0 })
	}
	hi := len(s.entries)
	if end != nil {
		hi = sort.Search(len(s.entries), func(i int) bool { return bytes.Compare(s.entries[i].key, end) >= 0 })
	}
	out := make([]entry, hi-lo)
	copy(out, s.entries[lo:hi])
	return &sliceIter{entries: out, pos: -1}
}

func (s *memSnapshot) Close() error { s.entries = nil; return nil }

type sliceIter struct {
	entries []entry
	pos     int
}

func (it *sliceIter) Next() bool {
	it.pos++
	return it.pos < len(it.entries)
}
func (it *sliceIter) Key() []byte   { return it.entries[it.pos].key }
func (it *sliceIter) Value() []byte { return it.entries[it.pos].value }
func (it *sliceIter) Err() error    { return nil }
func (it *sliceIter) Close() error  { it.entries = nil; return nil }
