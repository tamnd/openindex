// The shard map and routing table (architecture doc 10.2). The routing table
// maps a shard to its replica set and is the structure the root and aggregators
// consume to fan out (doc 08). The placement controller publishes it to the
// coordination store, and every serving node watches it, so a map change reaches
// the fleet in one watch event rather than a poll.
//
// The deliberate property here is the last-known-map fallback: a serving node
// keeps serving from the map it last saw if the control plane is briefly
// unreachable, because the map changes slowly and the query path must not depend
// on control-plane availability. The Router holds the map in memory and only
// ever advances it on a successful read.

package control

import (
	"context"
	"maps"
	"strconv"
	"strings"
	"sync"
)

// routePrefix is the store key prefix the routing table lives under. Each shard
// is one key so a single shard's reassignment is one small write and one watch
// event, rather than rewriting the whole map (doc 10.1 keeps writes small).
const routePrefix = "route/"

func routeKey(shard ShardID) string {
	return routePrefix + strconv.FormatUint(uint64(shard), 10)
}

// encodeNodes and decodeNodes serialize a replica set's node list. The encoding
// is a newline-joined list, which is compact and needs no schema; the control
// plane stores only pointers, so the values stay far under the store's
// request-size limit (doc 10.1).
func encodeNodes(nodes []NodeID) []byte {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = string(n)
	}
	return []byte(strings.Join(parts, "\n"))
}

func decodeNodes(b []byte) []NodeID {
	if len(b) == 0 {
		return nil
	}
	parts := strings.Split(string(b), "\n")
	out := make([]NodeID, len(parts))
	for i, p := range parts {
		out[i] = NodeID(p)
	}
	return out
}

// Publish writes an assignment to the store, one key per shard. It overwrites
// the shards in the assignment and does not delete shards absent from it, so a
// caller that shrinks the shard set deletes the dropped keys itself; in practice
// the shard count is stable (doc 10.3) and only replica sets change.
func Publish(ctx context.Context, store Store, a Assignment) error {
	for shard, rs := range a.Shards {
		if err := store.Put(ctx, routeKey(shard), encodeNodes(rs.Nodes), 0); err != nil {
			return err
		}
	}
	return nil
}

// Load reads the whole routing table from the store once. It is what a serving
// node calls on startup before it begins watching, so it starts with the
// current map rather than an empty one.
func Load(ctx context.Context, store Store) (Assignment, error) {
	kvs, err := store.List(ctx, routePrefix)
	if err != nil {
		return Assignment{}, err
	}
	a := NewAssignment()
	for k, v := range kvs {
		shard, ok := parseRouteKey(k)
		if !ok {
			continue
		}
		a.Shards[shard] = ReplicaSet{Shard: shard, Nodes: decodeNodes(v)}
	}
	return a, nil
}

func parseRouteKey(key string) (ShardID, bool) {
	s, ok := strings.CutPrefix(key, routePrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return ShardID(n), true
}

// Router holds a serving node's view of the routing table and keeps it current
// from the store. It is safe for concurrent use: the fan-out reads Lookup on the
// query path while the watch goroutine applies updates. It never regresses to an
// empty map on a control-plane hiccup, which is the last-known-map fallback.
type Router struct {
	mu      sync.RWMutex
	current Assignment
}

// NewRouter returns a Router seeded with an assignment (typically the result of
// Load). A nil-map assignment is replaced with an empty one so Lookup is safe
// before the first update.
func NewRouter(seed Assignment) *Router {
	if seed.Shards == nil {
		seed = NewAssignment()
	}
	return &Router{current: seed}
}

// Lookup returns the replica set for a shard and whether the shard is in the
// current map. It is the query-path read, so it takes only a read lock.
func (r *Router) Lookup(shard ShardID) (ReplicaSet, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rs, ok := r.current.Shards[shard]
	return rs, ok
}

// Snapshot returns a copy of the current assignment, for a caller that wants the
// whole map without holding the lock.
func (r *Router) Snapshot() Assignment {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := NewAssignment()
	maps.Copy(out.Shards, r.current.Shards)
	return out
}

// apply folds one store event into the current map: a put updates a shard's
// replica set, a delete removes the shard.
func (r *Router) apply(e Event) {
	shard, ok := parseRouteKey(e.Key)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e.Type {
	case EventPut:
		r.current.Shards[shard] = ReplicaSet{Shard: shard, Nodes: decodeNodes(e.Value)}
	case EventDelete:
		delete(r.current.Shards, shard)
	}
}

// Watch keeps the router current by applying store events until ctx is
// cancelled. A serving node runs it in a goroutine after seeding the router with
// Load. It does not re-Load on its own; a caller that wants to resynchronize
// after a long disconnect calls Load and seeds a new Router, so the running
// router never blanks its map.
func (r *Router) Watch(ctx context.Context, store Store) error {
	ch, err := store.Watch(ctx, routePrefix)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-ch:
			if !ok {
				return nil
			}
			r.apply(e)
		}
	}
}
