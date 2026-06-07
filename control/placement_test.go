package control

import (
	"slices"
	"testing"
)

func TestMembersRoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	l, _ := store.Grant(ctx, 10)
	if err := Register(ctx, store, "n3", l); err != nil {
		t.Fatal(err)
	}
	if err := Register(ctx, store, "n1", l); err != nil {
		t.Fatal(err)
	}
	got, err := Members(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "n1" || got[1] != "n3" {
		t.Fatalf("expected sorted [n1 n3], got %v", got)
	}
}

func TestLeaseExpiryDropsMember(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	l, _ := store.Grant(ctx, 10)
	_ = Register(ctx, store, "n1", l)
	if got, _ := Members(ctx, store); len(got) != 1 {
		t.Fatalf("expected the node registered, got %v", got)
	}
	store.Expire(l)
	if got, _ := Members(ctx, store); len(got) != 0 {
		t.Fatalf("a dead node's lease expiry should drop it, got %v", got)
	}
}

func TestCampaignSingleWinner(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	la, _ := store.Grant(ctx, 10)
	lb, _ := store.Grant(ctx, 10)
	wonA, err := Campaign(ctx, store, "placement", "a", la)
	if err != nil {
		t.Fatal(err)
	}
	wonB, _ := Campaign(ctx, store, "placement", "b", lb)
	if !wonA || wonB {
		t.Fatalf("exactly one should win: a=%v b=%v", wonA, wonB)
	}
	who, held, _ := Leader(ctx, store, "placement")
	if !held || who != "a" {
		t.Fatalf("a should hold leadership, got %q held=%v", who, held)
	}
}

func TestResignLetsNextWin(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	la, _ := store.Grant(ctx, 10)
	lb, _ := store.Grant(ctx, 10)
	_, _ = Campaign(ctx, store, "placement", "a", la)
	if err := Resign(ctx, store, "placement"); err != nil {
		t.Fatal(err)
	}
	if won, _ := Campaign(ctx, store, "placement", "b", lb); !won {
		t.Fatal("b should win after a resigns")
	}
}

func TestLeaderDeathReleasesLeadership(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	la, _ := store.Grant(ctx, 10)
	lb, _ := store.Grant(ctx, 10)
	_, _ = Campaign(ctx, store, "placement", "a", la)
	// The leader dies: its lease expires and the leader key vanishes.
	store.Expire(la)
	if _, held, _ := Leader(ctx, store, "placement"); held {
		t.Fatal("a dead leader should release the key")
	}
	if won, _ := Campaign(ctx, store, "placement", "b", lb); !won {
		t.Fatal("b should win once the dead leader's key is gone")
	}
}

func TestDesiredPlacesEveryShard(t *testing.T) {
	c := &PlacementController{Shards: 8, Replicas: 2}
	a := c.Desired([]NodeID{"n1", "n2", "n3"})
	if len(a.Shards) != 8 {
		t.Fatalf("expected 8 shards placed, got %d", len(a.Shards))
	}
	for s, rs := range a.Shards {
		if len(rs.Nodes) != 2 {
			t.Fatalf("shard %d got %d replicas, want 2", s, len(rs.Nodes))
		}
	}
}

func TestReconcilePublishesAndIsIdempotent(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	c := &PlacementController{Store: store, Shards: 16, Replicas: 2}
	l, _ := store.Grant(ctx, 10)
	for _, n := range []NodeID{"n1", "n2", "n3"} {
		_ = Register(ctx, store, n, l)
	}
	changed, err := c.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 16 {
		t.Fatalf("first reconcile should publish all 16 shards, published %d", changed)
	}
	// A second reconcile with no membership change writes nothing.
	if changed, _ = c.Reconcile(ctx); changed != 0 {
		t.Fatalf("a steady reconcile should be a no-op, rewrote %d", changed)
	}
}

func TestReconcileReassignsDeadNodesShards(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	c := &PlacementController{Store: store, Shards: 64, Replicas: 2}
	live, _ := store.Grant(ctx, 10)
	dying, _ := store.Grant(ctx, 10)
	for _, n := range []NodeID{"n1", "n2", "n3"} {
		_ = Register(ctx, store, n, live)
	}
	_ = Register(ctx, store, "n4", dying)
	if _, err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := Load(ctx, store)

	// n4 dies. Reconcile should rewrite only the shards that held n4.
	store.Expire(dying)
	changed, err := c.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed == 0 {
		t.Fatal("losing a node should reassign its shards")
	}
	after, _ := Load(ctx, store)
	for shard, rs := range after.Shards {
		if slices.Contains(rs.Nodes, "n4") {
			t.Fatalf("shard %d still references the dead node", shard)
		}
		// Shards that did not hold n4 must be untouched.
		held := slices.Contains(before.Shards[shard].Nodes, "n4")
		if !held && !slices.Equal(before.Shards[shard].Nodes, rs.Nodes) {
			t.Fatalf("shard %d moved though it did not hold the dead node", shard)
		}
	}
}

func TestRunReassignsOnNodeDeath(t *testing.T) {
	store := NewMemStore()
	ctx := t.Context()
	c := &PlacementController{Store: store, Shards: 32, Replicas: 2}
	live, _ := store.Grant(ctx, 10)
	dying, _ := store.Grant(ctx, 10)
	for _, n := range []NodeID{"n1", "n2", "n3"} {
		_ = Register(ctx, store, n, live)
	}
	_ = Register(ctx, store, "n4", dying)
	go func() { _ = c.Run(ctx) }()

	// Wait for the initial reconcile to publish the table.
	if !waitFor(func() bool { a, _ := Load(ctx, store); return len(a.Shards) == 32 }) {
		t.Fatal("controller never published the initial table")
	}
	// Kill n4 and let the membership watch drive a reconcile.
	store.Expire(dying)
	if !waitFor(func() bool {
		a, _ := Load(ctx, store)
		for _, rs := range a.Shards {
			if slices.Contains(rs.Nodes, "n4") {
				return false
			}
		}
		return true
	}) {
		t.Fatal("controller never drained the dead node from the table")
	}
}
