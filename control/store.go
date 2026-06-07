// The coordination store seam and its in-memory reference. In production this
// is etcd over clientv3 (architecture doc 10.1): Raft-backed, strongly
// consistent, with leases, watches, MVCC revisions, and leader election. The
// control plane uses exactly those primitives, so the seam exposes them and
// nothing more, and the MemStore reference implements them in process so the
// routing, health, and placement logic is testable without a cluster.

package control

import (
	"context"
	"slices"
	"strings"
	"sync"
)

// LeaseID identifies a lease: a node registers ephemeral keys under one, and
// when the lease expires (the node stopped keepaliving it) the store deletes
// those keys, which is the death signal placement reacts to (doc 10.4).
type LeaseID uint64

// EventType distinguishes a key write from a key deletion in a watch stream.
type EventType uint8

const (
	// EventPut is a key created or updated.
	EventPut EventType = iota
	// EventDelete is a key removed, including a key dropped by lease expiry.
	EventDelete
)

// Event is one change on a watched prefix. Value is nil for a delete.
type Event struct {
	Type  EventType
	Key   string
	Value []byte
}

// Store is the coordination store: a strongly-consistent key-value space with
// prefix watches and leases. It is the etcd subset the control plane uses (doc
// 10.1). Put with a non-zero lease ties the key's lifetime to that lease, so a
// node's keys vanish when it dies. Watch streams changes under a prefix until
// the context is cancelled, which is how the routing table reaches every
// serving node in one event rather than a poll (doc 10.2).
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte, lease LeaseID) error
	// Create writes key only if it is absent and reports whether it did. It is
	// the compare-and-claim that leader election needs (doc 10.1's elections):
	// the first candidate to create the leader key wins, the rest see it taken.
	// etcd does this with a transaction guarded on the key's create revision
	// being zero.
	Create(ctx context.Context, key string, value []byte, lease LeaseID) (bool, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) (map[string][]byte, error)
	Watch(ctx context.Context, prefix string) (<-chan Event, error)
	// Grant creates a lease. ttlSeconds is advisory in the reference store,
	// which expires a lease only on an explicit Expire call so tests are
	// deterministic; the etcd binding honors the real TTL.
	Grant(ctx context.Context, ttlSeconds int64) (LeaseID, error)
	Revoke(ctx context.Context, lease LeaseID) error
}

// MemStore is the in-process reference Store. It is not a toy: it keeps the
// lease-to-keys attachment, expires a lease by deleting its keys, and fans every
// change out to the matching watchers, which are the behaviors the control
// plane depends on. It does not replicate or persist, because the seam exists so
// the etcd binding provides those.
type MemStore struct {
	mu       sync.Mutex
	kv       map[string][]byte
	leaseOf  map[string]LeaseID   // key -> lease it is attached to
	keysOf   map[LeaseID][]string // lease -> its keys
	nextLid  LeaseID
	watchers []*watcher
}

type watcher struct {
	prefix string
	ch     chan Event
	ctx    context.Context
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		kv:      map[string][]byte{},
		leaseOf: map[string]LeaseID{},
		keysOf:  map[LeaseID][]string{},
		nextLid: 1,
	}
}

// Get returns the value at key and whether it was present.
func (m *MemStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[key]
	return v, ok, nil
}

// Put writes key and, if lease is non-zero, attaches the key to that lease so it
// is deleted when the lease is revoked or expires.
func (m *MemStore) Put(_ context.Context, key string, value []byte, lease LeaseID) error {
	m.mu.Lock()
	m.kv[key] = value
	if lease != 0 {
		// Re-attaching a key moves it to the new lease.
		if old, ok := m.leaseOf[key]; ok && old != lease {
			m.detach(old, key)
		}
		m.leaseOf[key] = lease
		m.keysOf[lease] = appendUnique(m.keysOf[lease], key)
	}
	m.fan(Event{Type: EventPut, Key: key, Value: value})
	m.mu.Unlock()
	return nil
}

// Create writes key only if it is absent, returning whether it created it. It is
// the atomic claim election relies on: two candidates that race to create the
// same leader key, only one wins.
func (m *MemStore) Create(_ context.Context, key string, value []byte, lease LeaseID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.kv[key]; ok {
		return false, nil
	}
	m.kv[key] = value
	if lease != 0 {
		m.leaseOf[key] = lease
		m.keysOf[lease] = appendUnique(m.keysOf[lease], key)
	}
	m.fan(Event{Type: EventPut, Key: key, Value: value})
	return true, nil
}

// Delete removes key.
func (m *MemStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	m.del(key)
	m.mu.Unlock()
	return nil
}

// List returns every key-value pair under prefix.
func (m *MemStore) List(_ context.Context, prefix string) (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range m.kv {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

// Watch returns a channel of changes under prefix. The channel closes when ctx
// is cancelled, and the watcher is dropped so it does not leak.
func (m *MemStore) Watch(ctx context.Context, prefix string) (<-chan Event, error) {
	m.mu.Lock()
	w := &watcher{prefix: prefix, ch: make(chan Event, 64), ctx: ctx}
	m.watchers = append(m.watchers, w)
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		m.dropWatcher(w)
		close(w.ch)
		m.mu.Unlock()
	}()
	return w.ch, nil
}

// Grant creates a lease.
func (m *MemStore) Grant(_ context.Context, _ int64) (LeaseID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextLid
	m.nextLid++
	m.keysOf[id] = nil
	return id, nil
}

// Revoke ends a lease and deletes its keys, the same path lease expiry takes.
func (m *MemStore) Revoke(_ context.Context, lease LeaseID) error {
	m.mu.Lock()
	m.expire(lease)
	m.mu.Unlock()
	return nil
}

// Expire simulates a lease timing out: it deletes the lease's keys and fans the
// deletions to watchers, exactly as a missed keepalive does in etcd. Tests call
// it to drive a node death deterministically.
func (m *MemStore) Expire(lease LeaseID) {
	m.mu.Lock()
	m.expire(lease)
	m.mu.Unlock()
}

// expire deletes a lease's keys. The caller holds the lock.
func (m *MemStore) expire(lease LeaseID) {
	keys := m.keysOf[lease]
	delete(m.keysOf, lease)
	for _, k := range keys {
		if m.leaseOf[k] == lease {
			m.del(k)
		}
	}
}

// del removes a key, detaches it from any lease, and fans the delete. The caller
// holds the lock.
func (m *MemStore) del(key string) {
	if _, ok := m.kv[key]; !ok {
		return
	}
	delete(m.kv, key)
	if lid, ok := m.leaseOf[key]; ok {
		m.detach(lid, key)
		delete(m.leaseOf, key)
	}
	m.fan(Event{Type: EventDelete, Key: key})
}

// detach removes key from a lease's key list. The caller holds the lock.
func (m *MemStore) detach(lease LeaseID, key string) {
	ks := m.keysOf[lease]
	for i, k := range ks {
		if k == key {
			m.keysOf[lease] = append(ks[:i], ks[i+1:]...)
			return
		}
	}
}

// fan delivers an event to every watcher whose prefix matches. The caller holds
// the lock. A watcher with a full buffer drops the event rather than blocking
// the writer, the same back-pressure choice etcd makes for a slow watcher.
func (m *MemStore) fan(e Event) {
	for _, w := range m.watchers {
		if !strings.HasPrefix(e.Key, w.prefix) {
			continue
		}
		select {
		case w.ch <- e:
		default:
		}
	}
}

// dropWatcher removes w from the watcher list. The caller holds the lock.
func (m *MemStore) dropWatcher(w *watcher) {
	for i, x := range m.watchers {
		if x == w {
			m.watchers = append(m.watchers[:i], m.watchers[i+1:]...)
			return
		}
	}
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
