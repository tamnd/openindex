// Rendezvous (highest-random-weight) hashing for replica-to-node placement
// (architecture doc 10.3). For each (shard, node) pair it computes a weight and
// picks the top nodes by weight, which gives two properties the placement
// controller wants at the few-hundred-node scale of a serving cluster:
//
//   - When a node is removed, only the shards that were on it move, and they
//     spread evenly across the remaining nodes, rather than piling onto one ring
//     neighbor the way a hash ring would.
//   - The choice is a pure function of the shard, the node set, and the replica
//     count, so any controller that sees the same live set computes the same
//     placement without coordination.
//
// Document-to-shard assignment uses jump hashing instead (jump.go); rendezvous
// is for the smaller, churn-heavy replica placement.

package control

import (
	"hash/fnv"
	"slices"
)

// Place returns the nodes that should hold the replicas of shard: the replicas
// highest-weight nodes from the candidate set, in descending weight order so the
// first is the primary. It returns every candidate when replicas exceeds the
// candidate count, and an empty slice when there are no candidates. The result
// is deterministic: ties break toward the smaller node id so two controllers
// never disagree.
func Place(shard ShardID, candidates []NodeID, replicas int) []NodeID {
	if len(candidates) == 0 || replicas <= 0 {
		return nil
	}
	type weighted struct {
		node NodeID
		w    uint64
	}
	ws := make([]weighted, len(candidates))
	for i, n := range candidates {
		ws[i] = weighted{node: n, w: weight(shard, n)}
	}
	slices.SortFunc(ws, func(a, b weighted) int {
		if a.w != b.w {
			if a.w > b.w {
				return -1
			}
			return 1
		}
		// Tie-break toward the smaller node id for determinism.
		if a.node < b.node {
			return -1
		}
		if a.node > b.node {
			return 1
		}
		return 0
	})
	n := min(replicas, len(ws))
	out := make([]NodeID, n)
	for i := range n {
		out[i] = ws[i].node
	}
	return out
}

// weight is the rendezvous score of placing shard on node. It hashes the node
// to a seed, mixes the shard in, and runs the result through the splitmix64
// finalizer. The finalizer matters: a plain concatenated hash of two short,
// near-identical node strings is not uniform enough across nodes, which biases
// rendezvous toward a few nodes and breaks the even rebalance. splitmix64's
// avalanche gives each (shard, node) pair an independent, uniform weight, which
// is what makes the displaced shards spread evenly when a node leaves.
func weight(shard ShardID, node NodeID) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(node))
	x := h.Sum64() ^ (uint64(shard) * 0x9e3779b97f4a7c15)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
