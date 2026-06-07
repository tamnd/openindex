package serve

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
)

// FanoutConfig tunes a scatter-gather.
type FanoutConfig struct {
	// MaxConcurrent bounds how many leaf RPCs are in flight at once. Zero means
	// unbounded, which the root should never use; aggregators set it to keep a
	// wide fan-out from exhausting the connection pool.
	MaxConcurrent int
	// PerShardTimeout is the sub-deadline each leaf RPC gets, derived from the
	// query's remaining budget. A leaf that misses it degrades to a missing
	// partial result, not a query failure.
	PerShardTimeout time.Duration
}

// Gathered is the outcome of a scatter: one slot per shard in shard order, with
// OK[i] reporting whether shard i answered in time. A failed or timed-out shard
// leaves a zero Response and OK[i] false, so the caller sees which shards
// contributed and can apply a good-enough cutoff.
type Gathered struct {
	Responses []Response
	OK        []bool
}

// Responded returns how many shards answered.
func (g Gathered) Responded() int {
	var n int
	for _, ok := range g.OK {
		if ok {
			n++
		}
	}
	return n
}

// OKResponses returns just the responses that arrived, dropping the gaps. It is
// the input MergeTopK wants.
func (g Gathered) OKResponses() []Response {
	out := make([]Response, 0, len(g.Responses))
	for i, ok := range g.OK {
		if ok {
			out = append(out, g.Responses[i])
		}
	}
	return out
}

// Scatter fans req out to every leaf and gathers the replies, following the
// fixed shape of doc 08.1: an errgroup bounded by MaxConcurrent, results written
// into a pre-sized slice indexed by shard (no channel, no mutex on the hot
// path), each RPC given a sub-deadline off the parent context with defer cancel
// so a straggler's work is freed the moment its budget passes, and a leaf error
// degraded to a missing partial result rather than a group abort. Important
// documents are replicated across leaves (doc 05), so a missing shard rarely
// holds the unique best result, which is what makes partial tolerance safe.
//
// Scatter returns when every leaf has either answered or hit its sub-deadline.
// The parent context's own deadline still applies: if it fires first, the
// outstanding RPCs are cancelled and reported as not OK.
func Scatter(ctx context.Context, leaves []Leaf, req Request, cfg FanoutConfig) Gathered {
	g := Gathered{
		Responses: make([]Response, len(leaves)),
		OK:        make([]bool, len(leaves)),
	}
	grp, gctx := errgroup.WithContext(ctx)
	if cfg.MaxConcurrent > 0 {
		grp.SetLimit(cfg.MaxConcurrent)
	}
	for i, leaf := range leaves {
		grp.Go(func() error {
			cctx := gctx
			var cancel context.CancelFunc
			if cfg.PerShardTimeout > 0 {
				cctx, cancel = context.WithTimeout(gctx, cfg.PerShardTimeout)
				defer cancel()
			}
			r, err := leaf.Search(cctx, req)
			if err != nil {
				return nil // partial-result tolerance, not a group abort
			}
			g.Responses[i] = r
			g.OK[i] = true
			return nil
		})
	}
	_ = grp.Wait()
	return g
}
