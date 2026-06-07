package serve

import (
	"context"
	"errors"
	"math"

	"openindex"
)

// ErrInsufficientShards is returned when too few shards answered for the result
// to be trustworthy. It is the floor under the good-enough cutoff: dropping a
// slow shard is fine, but a page built from a small minority of shards is
// missing too much to serve.
var ErrInsufficientShards = errors.New("serve: too few shards responded")

// Coordinator is a serving-tree node that fans a query to its children and
// merges their replies. The same type is the root over aggregators and an
// aggregator over leaves; only the children differ. It applies the good-enough
// cutoff from doc 08.2, which is not a separate timer but the combination of
// the per-shard sub-deadline (slow shards simply do not answer in time) and a
// minimum-responded floor checked after the gather.
type Coordinator struct {
	children []Leaf
	cfg      FanoutConfig
	// minResponded is the fraction of children that must answer for the result
	// to be served, in (0,1]. A value of 0.95 returns once 95 percent have
	// replied and treats the rest as the tail to drop.
	minResponded float64
}

// NewCoordinator builds a node over the given children. minResponded is clamped
// to (0,1]; a zero or negative value selects 1.0 (every child must answer),
// which is the strict default a caller relaxes deliberately.
func NewCoordinator(children []Leaf, cfg FanoutConfig, minResponded float64) *Coordinator {
	if minResponded <= 0 || minResponded > 1 {
		minResponded = 1
	}
	return &Coordinator{children: children, cfg: cfg, minResponded: minResponded}
}

// Search fans req out to the children, applies the good-enough cutoff, and
// returns the merged global top-k. It returns ErrInsufficientShards when fewer
// than the required fraction of children answered, so a degraded page is never
// silently served as if it were complete.
func (c *Coordinator) Search(ctx context.Context, req Request) ([]openindex.Result, error) {
	if len(c.children) == 0 {
		return nil, nil
	}
	g := Scatter(ctx, c.children, req, c.cfg)

	need := max(int(math.Ceil(c.minResponded*float64(len(c.children)))), 1)
	if g.Responded() < need {
		// Surface the context error if that is why shards were lost, since a
		// cancelled query is a different failure from a flaky fleet.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrInsufficientShards
	}
	return MergeTopK(g.OKResponses(), req.K), nil
}
