// Package control is the control plane the data plane assumes (architecture doc
// 10): the coordination store, the shard maps and routing tables, the
// consistent-hashing placement, and the lease-based health that ties them
// together. It runs as the placement controller and the merge scheduler
// singletons (doc 02), backed by a coordination store.
//
// The discipline that keeps the control plane small is that it holds pointers,
// maps, and health, never document data. The shard contents live in object
// storage and Pebble (doc 03); the control plane holds only the map from a
// shard to the nodes that serve it, the node health, and the config. That is a
// metadata workload of thousands of keys and tens of writes per second, so it
// can be strongly consistent and stay off the query hot path.
//
// In production the store is etcd over the clientv3 API (doc 10.1), behind the
// Store seam (store.go). Placement is two hashing schemes chosen by churn
// profile: jump consistent hashing for document-to-shard (jump.go) and
// rendezvous hashing for replica-to-node (rendezvous.go). The routing table
// (routing.go) is published to the store and watched by every serving node, and
// liveness is a lease each node keepalives (health.go), so a lost lease is the
// authoritative death signal that frees a node's shards for reassignment by the
// placement controller (placement.go).
package control

import "slices"

// NodeID names a serving node: one machine in the serving tree (doc 08). It is
// the unit placement assigns shards to and the unit a lease tracks the liveness
// of.
type NodeID string

// ShardID names a shard: one slice of the partitioned index. It mirrors the
// serving tier's shard id (doc 08); the control plane owns the authoritative map
// from a shard to the nodes that hold its replicas.
type ShardID uint32

// Replica is one copy of a shard placed on a node. A shard has several replicas
// for availability and for QPS headroom (more replicas than shards, doc 08.7),
// and the routing table fans a query to one replica per shard.
type Replica struct {
	Shard ShardID
	Node  NodeID
}

// ReplicaSet is the set of nodes holding a shard's replicas, in a stable order
// so two readers of the same map fan out the same way. The placement controller
// owns it; the serving tree consumes it.
type ReplicaSet struct {
	Shard ShardID
	Nodes []NodeID
}

// Assignment is a full placement: every shard mapped to its replica set. It is
// the value the placement controller publishes and the serving nodes watch, the
// in-memory form of the routing table (doc 10.2).
type Assignment struct {
	Shards map[ShardID]ReplicaSet
}

// NewAssignment returns an empty assignment ready to fill.
func NewAssignment() Assignment {
	return Assignment{Shards: map[ShardID]ReplicaSet{}}
}

// Nodes returns the sorted set of distinct nodes that hold any replica, the set
// the controller treats as currently carrying load. Sorting makes the result
// deterministic for callers that diff successive assignments.
func (a Assignment) Nodes() []NodeID {
	seen := map[NodeID]bool{}
	for _, rs := range a.Shards {
		for _, n := range rs.Nodes {
			seen[n] = true
		}
	}
	out := make([]NodeID, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// ShardsOn returns the shards that have a replica on the given node, sorted. It
// is what the controller reads to know which shards a dead node was carrying so
// it can reassign exactly those.
func (a Assignment) ShardsOn(node NodeID) []ShardID {
	var out []ShardID
	for shard, rs := range a.Shards {
		if slices.Contains(rs.Nodes, node) {
			out = append(out, shard)
		}
	}
	slices.Sort(out)
	return out
}
