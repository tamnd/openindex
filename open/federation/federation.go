// Package federation lets independent operators host different signed shards and
// have a query fan out across them (implementation doc 11.4). It is not open
// peer-to-peer: full P2P search collapses relevance and latency across untrusted,
// slow, churning peers, which doc 01's non-goals rule out. Federation here
// decentralizes only where it does not cost the bar:
//
//   - A partition is trusted before it is queried. The gate (doc 11.5) admits a
//     partition only if its operator is in the key registry and its reputation
//     clears a floor, which is the Sybil resistance that an open, downloadable,
//     federation-hostable index needs and cannot retrofit.
//   - A partition that cannot keep up is dropped. Each partition gets a sub-
//     deadline, and the merge serves whatever arrived in time, so a slow
//     federated shard degrades the result rather than breaking the deadline (the
//     good-enough cutoff, doc 08.2).
//   - First-party serving always meets the contract on its own. The federation is
//     reach and trust on top, never the only path to an answer (doc 01).
//
// A federated partition is just a leaf the root does not own: it answers the same
// Leaf.Search (doc 02) and carries its signed snapshot id, so the existing
// scatter-gather tree (doc 08) is the federation transport.
package federation

import (
	"context"
	"slices"
	"sync"
	"time"

	"openindex"
	"openindex/open"
)

// Leaf is the search seam a partition exposes, the same shape as the first-party
// Leaf.Search (doc 02): given a query and a result count, return the top hits or
// an error. A partition that errors or misses its deadline is dropped, so the
// seam does not need a separate health signal.
type Leaf interface {
	Search(ctx context.Context, query string, k int) ([]openindex.Result, error)
}

// Partition is one federated shard: the operator that hosts it, the signed
// snapshot it serves, and the leaf that answers queries over it. The snapshot id
// is what a consumer checks against the operator's signed artifact (doc 11.2).
type Partition struct {
	Operator OperatorID
	Snapshot openindex.SnapshotID
	Leaf     Leaf
}

// OperatorID is re-exported from package open so callers of the federation gate
// do not import both for one identifier.
type OperatorID = open.OperatorID

// Gate decides which partitions are trusted enough to query. A partition is
// trusted only when its operator is in the key registry (so its artifacts can be
// verified) and its reputation clears MinReputation. Both conditions are the
// Sybil defense: an unknown operator and a low-reputation operator are excluded
// before they can influence a result (doc 11.5).
type Gate struct {
	registry      *open.Registry
	reputation    map[OperatorID]float64
	MinReputation float64
}

// NewGate returns a gate over an operator-key registry, admitting operators whose
// reputation is at least minReputation.
func NewGate(registry *open.Registry, minReputation float64) *Gate {
	return &Gate{
		registry:      registry,
		reputation:    map[OperatorID]float64{},
		MinReputation: minReputation,
	}
}

// SetReputation records an operator's reputation, the score the gate compares
// against its floor. Reputation is earned over time (consistent, verifiable
// shards) and is the lever an operator that ships a bad shard loses.
func (g *Gate) SetReputation(op OperatorID, score float64) {
	g.reputation[op] = score
}

// Trusted reports whether a partition may be queried: its operator is known to
// the registry and its reputation clears the floor.
func (g *Gate) Trusted(p Partition) bool {
	if _, known := g.registry.Key(p.Operator); !known {
		return false
	}
	return g.reputation[p.Operator] >= g.MinReputation
}

// Federator fans a query out across trusted partitions and merges the results.
// Deadline is the per-partition sub-deadline; a partition slower than that is
// dropped. K is how many results the merge returns.
type Federator struct {
	Gate     *Gate
	Deadline time.Duration
	K        int
}

// partial is one partition's contribution: its results, or an error that drops
// it from the merge.
type partial struct {
	results []openindex.Result
	err     error
}

// Search queries every trusted partition with its own sub-deadline, merges the
// results that arrived in time, and returns the top K. Untrusted partitions are
// skipped before any query. A partition that errors or exceeds its deadline
// contributes nothing rather than failing the whole fan-out, which is the
// graceful degradation the latency budget requires. The overall wait is bounded
// by the deadline plus a small slack, so one misbehaving leaf that ignores its
// context cannot stall the merge.
func (f *Federator) Search(ctx context.Context, query string, partitions []Partition) []openindex.Result {
	trusted := make([]Partition, 0, len(partitions))
	for _, p := range partitions {
		if f.Gate.Trusted(p) {
			trusted = append(trusted, p)
		}
	}
	if len(trusted) == 0 {
		return nil
	}

	ch := make(chan partial, len(trusted))
	var wg sync.WaitGroup
	for _, p := range trusted {
		wg.Add(1)
		go func(p Partition) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, f.Deadline)
			defer cancel()
			res, err := p.Leaf.Search(pctx, query, f.K)
			ch <- partial{results: res, err: err}
		}(p)
	}

	var merged []openindex.Result
	timer := time.NewTimer(f.Deadline + 50*time.Millisecond)
	defer timer.Stop()
	for collected := 0; collected < len(trusted); collected++ {
		select {
		case pr := <-ch:
			if pr.err == nil {
				merged = append(merged, pr.results...)
			}
		case <-timer.C:
			// The budget is spent. Serve with what arrived; the rest are dropped.
			return topK(merged, f.K)
		case <-ctx.Done():
			return topK(merged, f.K)
		}
	}
	return topK(merged, f.K)
}

// topK sorts the merged results best-first and keeps at most k. Score.Less ranks
// a higher score first, so it is the comparator the sort wants.
func topK(results []openindex.Result, k int) []openindex.Result {
	slices.SortFunc(results, func(a, b openindex.Result) int {
		switch {
		case a.Score.Less(b.Score):
			return -1
		case b.Score.Less(a.Score):
			return 1
		default:
			return 0
		}
	})
	if k > 0 && len(results) > k {
		results = results[:k]
	}
	return results
}
