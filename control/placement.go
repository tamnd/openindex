// The placement controller (architecture doc 10.3 and 10.4). It is the single
// leader that turns the live node set into the routing table: for each shard it
// places a replica set with rendezvous hashing, then publishes the shards that
// changed. Driving placement off membership is what makes a node death
// self-healing: the dead node's lease expires, its registration key vanishes,
// the controller's membership watch fires, and the controller reassigns only the
// shards that node held, spread evenly across the survivors (rendezvous.go).
//
// The controller assumes it holds leadership (election.go). Running it without
// the election would let two controllers publish conflicting maps, which is the
// exact failure the single-leader rule in doc 10.3 forbids.

package control

import (
	"context"
	"slices"
)

// PlacementController computes and publishes the routing table. Shards is the
// stable shard count (the jump-bucket count of doc 10.3, which never shrinks);
// Replicas is how many nodes hold each shard.
type PlacementController struct {
	Store    Store
	Shards   int
	Replicas int
}

// Desired is the assignment that should hold for a given live node set: every
// shard placed on its top Replicas nodes by rendezvous weight. It is a pure
// function of the shard count, replica count, and node set, so the controller
// and any observer compute the same target from the same membership.
func (c *PlacementController) Desired(members []NodeID) Assignment {
	a := NewAssignment()
	for s := range c.Shards {
		shard := ShardID(s)
		a.Shards[shard] = ReplicaSet{Shard: shard, Nodes: Place(shard, members, c.Replicas)}
	}
	return a
}

// Reconcile reads the live membership, computes the desired assignment, and
// publishes only the shards whose replica set changed. It returns how many
// shards it rewrote. Publishing the diff rather than the whole map keeps writes
// small (doc 10.1) and means a single node death touches only that node's
// shards, so the watch traffic to the fleet stays proportional to the churn.
func (c *PlacementController) Reconcile(ctx context.Context) (int, error) {
	members, err := Members(ctx, c.Store)
	if err != nil {
		return 0, err
	}
	current, err := Load(ctx, c.Store)
	if err != nil {
		return 0, err
	}
	desired := c.Desired(members)
	diff := NewAssignment()
	for shard, rs := range desired.Shards {
		if !slices.Equal(current.Shards[shard].Nodes, rs.Nodes) {
			diff.Shards[shard] = rs
		}
	}
	if len(diff.Shards) == 0 {
		return 0, nil
	}
	if err := Publish(ctx, c.Store, diff); err != nil {
		return 0, err
	}
	return len(diff.Shards), nil
}

// Run reconciles once, then watches the membership prefix and reconciles on
// every change until ctx is cancelled. A node death is one such change (its
// lease expiry deletes its node key), so the controller reassigns the dead
// node's shards without polling. The caller runs this only while it holds
// leadership; on losing leadership it cancels ctx and stops publishing.
func (c *PlacementController) Run(ctx context.Context) error {
	if _, err := c.Reconcile(ctx); err != nil {
		return err
	}
	ch, err := c.Store.Watch(ctx, nodePrefix)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return nil
			}
			if _, err := c.Reconcile(ctx); err != nil {
				return err
			}
		}
	}
}
