package control

import (
	"testing"
	"time"
)

func TestPublishLoadRoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	a := NewAssignment()
	a.Shards[0] = ReplicaSet{Shard: 0, Nodes: []NodeID{"n1", "n2"}}
	a.Shards[7] = ReplicaSet{Shard: 7, Nodes: []NodeID{"n3"}}
	if err := Publish(ctx, store, a); err != nil {
		t.Fatal(err)
	}
	got, err := Load(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Shards) != 2 {
		t.Fatalf("expected 2 shards, got %d", len(got.Shards))
	}
	if rs := got.Shards[0]; len(rs.Nodes) != 2 || rs.Nodes[0] != "n1" || rs.Nodes[1] != "n2" {
		t.Fatalf("shard 0 round-trip wrong: %+v", rs)
	}
	if rs := got.Shards[7]; len(rs.Nodes) != 1 || rs.Nodes[0] != "n3" {
		t.Fatalf("shard 7 round-trip wrong: %+v", rs)
	}
}

func TestEncodeDecodeNodesEmpty(t *testing.T) {
	if got := decodeNodes(encodeNodes(nil)); got != nil {
		t.Fatalf("empty node list should round-trip to nil, got %v", got)
	}
}

func TestRouterLookup(t *testing.T) {
	a := NewAssignment()
	a.Shards[3] = ReplicaSet{Shard: 3, Nodes: []NodeID{"n1"}}
	r := NewRouter(a)
	if rs, ok := r.Lookup(3); !ok || rs.Nodes[0] != "n1" {
		t.Fatalf("lookup failed: %+v %v", rs, ok)
	}
	if _, ok := r.Lookup(99); ok {
		t.Fatal("an absent shard should miss")
	}
}

func TestRouterNilSeedIsSafe(t *testing.T) {
	r := NewRouter(Assignment{})
	if _, ok := r.Lookup(0); ok {
		t.Fatal("a nil-seed router should look up to a clean miss")
	}
}

func TestRouterWatchAppliesUpdates(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	r := NewRouter(NewAssignment())
	go func() { _ = r.Watch(ctx, store) }()
	// Give the watch a moment to register before the first write.
	time.Sleep(20 * time.Millisecond)

	a := NewAssignment()
	a.Shards[1] = ReplicaSet{Shard: 1, Nodes: []NodeID{"n4", "n5"}}
	if err := Publish(ctx, store, a); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { _, ok := r.Lookup(1); return ok }) {
		t.Fatal("router never saw the published shard")
	}
	rs, _ := r.Lookup(1)
	if len(rs.Nodes) != 2 || rs.Nodes[0] != "n4" {
		t.Fatalf("router applied the wrong replica set: %+v", rs)
	}

	// A delete should remove the shard from the router.
	_ = store.Delete(ctx, routeKey(1))
	if !waitFor(func() bool { _, ok := r.Lookup(1); return !ok }) {
		t.Fatal("router never saw the shard removed")
	}
}

func TestRouterKeepsLastKnownMap(t *testing.T) {
	// After the watch context ends, the router still answers from its last map,
	// which is the fallback that keeps the query path off the control plane.
	store := NewMemStore()
	a := NewAssignment()
	a.Shards[2] = ReplicaSet{Shard: 2, Nodes: []NodeID{"n1"}}
	if err := Publish(t.Context(), store, a); err != nil {
		t.Fatal(err)
	}
	seed, _ := Load(t.Context(), store)
	r := NewRouter(seed)
	if _, ok := r.Lookup(2); !ok {
		t.Fatal("router should answer from its seed map with no live watch")
	}
}

func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
