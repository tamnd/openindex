// Package storage is the embedded key-value substrate the whole engine stands
// on: the WebTable document store (storage doc 03.2), the link-graph adjacency
// index (03.4), and the small metadata stores. It is a leaf in the dependency
// order - index, rank, and serve depend on it, it depends on nothing in the
// module except the shared types and telemetry (impl spec 02.3).
//
// The package is built against the Engine seam rather than a concrete store.
// The implementation spec chose one engine per workload shape - Pebble for the
// WebTable (stable read latency and low space amplification at billions of
// rows), Badger for the large-value blob store, bbolt for small config (storage
// doc 03.1). Those production engines plug in behind Engine; the in-process
// MemEngine here is the reference implementation that the WebTable and link
// graph are unit-tested against, so the schema and access patterns are
// exercised without standing up a real LSM.
package storage

// Engine is an ordered, byte-keyed store with range scans and consistent
// snapshots. Keys sort lexicographically, which is the property the WebTable
// row-key layout (03.2) relies on to keep a site's pages and a domain's sites
// contiguous. All methods are safe for concurrent use.
type Engine interface {
	// Get returns the value for key and whether it was present. The returned
	// slice must not be retained by the engine after Get returns; callers may
	// hold and mutate it.
	Get(key []byte) (value []byte, ok bool, err error)
	// Set stores value under key, overwriting any prior value.
	Set(key, value []byte) error
	// Delete removes key; deleting an absent key is not an error.
	Delete(key []byte) error
	// Scan returns an iterator over the half-open key range [start, end). A nil
	// end means "to the end of the keyspace"; pass a key-prefix successor (see
	// PrefixEnd) to scan a prefix.
	Scan(start, end []byte) Iterator
	// Apply commits a batch of writes atomically.
	Apply(*Batch) error
	// Snapshot pins a consistent read-only view. It is the unit of consistency
	// for building a serving index or an open export (03.5): the indexer reads a
	// snapshot while crawl writes continue against the live engine.
	Snapshot() Snapshot
	// Close releases the engine's resources.
	Close() error
}

// Snapshot is a frozen, read-only view of the engine at one instant. Reads from
// it never observe writes that landed after it was taken.
type Snapshot interface {
	Get(key []byte) (value []byte, ok bool, err error)
	Scan(start, end []byte) Iterator
	// Close releases the snapshot. Holding a snapshot pins the versions it can
	// see, so it is closed promptly once a build finishes.
	Close() error
}

// Iterator walks key/value pairs in ascending key order. The usual shape is:
//
//	it := eng.Scan(start, end)
//	defer it.Close()
//	for it.Next() {
//	    k, v := it.Key(), it.Value()
//	    ...
//	}
//	if err := it.Err(); err != nil { ... }
//
// Key and Value return slices owned by the iterator and valid only until the
// next call to Next; a caller that retains them copies first.
type Iterator interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	Close() error
}

// Batch accumulates writes to be applied atomically by Engine.Apply. It mirrors
// the LSM commit that gives the WebTable its crash consistency (03.5): a crawl
// fetch that writes the contents, meta, and link cells of one page commits them
// as a unit so a reader never sees a half-written row.
type Batch struct {
	ops []op
}

type op struct {
	key, value []byte
	del        bool
}

// Set queues a write. The key and value are copied, so the caller may reuse its
// buffers immediately.
func (b *Batch) Set(key, value []byte) {
	b.ops = append(b.ops, op{key: clone(key), value: clone(value)})
}

// Delete queues a deletion.
func (b *Batch) Delete(key []byte) {
	b.ops = append(b.ops, op{key: clone(key), del: true})
}

// Len reports the number of queued operations.
func (b *Batch) Len() int { return len(b.ops) }

// Reset clears the batch for reuse, keeping the backing capacity.
func (b *Batch) Reset() { b.ops = b.ops[:0] }

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// PrefixEnd returns the smallest key that sorts after every key with the given
// prefix, i.e. the exclusive end for a prefix scan: Scan(prefix, PrefixEnd(prefix))
// visits exactly the keys that start with prefix. A nil or all-0xFF prefix has
// no finite successor, so PrefixEnd returns nil meaning "to the end".
func PrefixEnd(prefix []byte) []byte {
	end := clone(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 0xFF {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}
