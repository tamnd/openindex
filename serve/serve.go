// Package serve is the query-serving tier (architecture doc 08): the gRPC
// fan-out from the root through aggregators to leaves, the tail-latency
// techniques that hold the P99 budget across a fleet large enough that some
// machine is always slow, the caching that carries the load, and the deadline
// propagation that ties it together. It runs as the cmd/leaf, cmd/aggregator,
// cmd/root, and cmd/mixer roles.
//
// The defining problem of the tier is the tail: a single leaf's 99th-percentile
// latency becomes the root's median once a query fans out to enough leaves, so
// waiting for the slowest is not an option. The pieces here address that, each
// behind a seam so the policy is testable in-process without a network: Scatter
// fans a request out with a per-shard sub-deadline and tolerates a missing
// shard rather than failing the query (fanout.go), MergeTopK reassembles the
// global ranking (merge.go), Hedge fires a backup replica when the first is
// slow (hedge.go), and the Loader collapses a cache stampede to one backend
// call (cache.go).
package serve

import (
	"context"

	"openindex"
)

// ShardID names a shard: one slice of the partitioned index. The index is
// micro-partitioned (many more shards than machines, doc 08.2) so load sheds in
// fine increments and a slow shard is a small fraction of the query.
type ShardID uint32

// Request is a query as it travels down the serving tree. It carries the
// snapshot id so a leaf can reject a format it cannot read (doc 02.4), and the
// requested result count so each node returns only what its parent can use.
type Request struct {
	Query    string
	K        int
	Snapshot openindex.SnapshotID
}

// Response is one node's contribution to the query: its top results, already
// ranked. An aggregator's response is the merge of its leaves' responses; the
// root's response is the merge of its aggregators'.
type Response struct {
	Results []openindex.Result
}

// Leaf is one searchable shard replica. The production leaf is a long-lived
// gRPC client to a replica set (doc 08.6); a test uses an in-process fake. A
// Leaf must honor the context deadline: the fan-out gives it a sub-deadline and
// counts on it to return or error by then rather than run past it.
type Leaf interface {
	Search(ctx context.Context, req Request) (Response, error)
}
