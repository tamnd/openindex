package graph

// DefaultDamping is the standard PageRank damping factor: the probability that
// the random surfer follows a link rather than teleporting.
const DefaultDamping = 0.85

// Config tunes a PageRank run.
type Config struct {
	// Damping is d in the PageRank recurrence. Zero selects DefaultDamping.
	Damping float32
	// Tolerance is the convergence threshold on the L1 residual between
	// successive rank vectors. Zero selects a small default.
	Tolerance float32
	// MaxSupersteps caps the iteration count so a non-converging run still
	// terminates. Zero selects a default sized for web-scale graphs.
	MaxSupersteps int
	// Teleport is the restart distribution: the vector the random surfer jumps
	// to when teleporting, and the vector dangling-node mass is redistributed
	// to. It must be non-negative and sum to 1. Nil selects the uniform
	// distribution 1/N, which is plain PageRank; a biased vector turns the same
	// executor into TrustRank.
	Teleport []float32
}

func (c Config) withDefaults(n int) Config {
	if c.Damping <= 0 {
		c.Damping = DefaultDamping
	}
	if c.Tolerance <= 0 {
		c.Tolerance = 1e-6
	}
	if c.MaxSupersteps <= 0 {
		c.MaxSupersteps = 100
	}
	if c.Teleport == nil {
		c.Teleport = make([]float32, n)
		uniform := float32(1) / float32(n)
		for i := range c.Teleport {
			c.Teleport[i] = uniform
		}
	}
	return c
}

// Stats reports how a run terminated.
type Stats struct {
	// Supersteps is the number of iterations performed.
	Supersteps int
	// Residual is the final L1 change between successive rank vectors.
	Residual float32
	// Converged is true if the run stopped because Residual fell below the
	// tolerance rather than because it hit MaxSupersteps.
	Converged bool
}

// PageRank computes the stationary rank vector by power iteration in the Pregel
// model:
//
//	PR(u) = (1-d)*T[u] + d * ( Sum_{v->u} PR(v)/L(v) + danglingMass*T[u] )
//
// where T is the teleport vector and danglingMass is the rank held by vertices
// with no out-edges, redistributed to T so no mass leaks. Each superstep is one
// vertex-centric round: every vertex pushes PR(v)/outdeg(v) along its out-edges,
// the combiner sums those contributions per destination, and the aggregator
// computes the global residual to test convergence. The returned vector sums to
// approximately 1.
func PageRank(g *Graph, cfg Config) ([]float32, Stats) {
	n := g.NumVertices()
	if n == 0 {
		return nil, Stats{Converged: true}
	}
	cfg = cfg.withDefaults(n)
	d := cfg.Damping
	teleport := cfg.Teleport

	rank := make([]float32, n)
	copy(rank, teleport)
	msg := make([]float32, n) // combiner accumulator, reused each superstep

	var stats Stats
	for step := 1; step <= cfg.MaxSupersteps; step++ {
		for i := range msg {
			msg[i] = 0
		}
		// Push phase: each vertex sends PR(v)/outdeg(v) to each out-neighbor,
		// summed at the destination by the combiner. Dangling vertices send
		// their whole rank to the teleport vector instead.
		var dangling float32
		for v := range n {
			deg := len(g.out[v])
			if deg == 0 {
				dangling += rank[v]
				continue
			}
			share := rank[v] / float32(deg)
			for _, u := range g.out[v] {
				msg[u] += share
			}
		}
		// Compute phase plus residual aggregation.
		var residual float32
		base := 1 - d
		for u := range n {
			next := base*teleport[u] + d*(msg[u]+dangling*teleport[u])
			residual += abs(next - rank[u])
			rank[u] = next
		}
		stats.Supersteps = step
		stats.Residual = residual
		if residual < cfg.Tolerance {
			stats.Converged = true
			break
		}
	}
	return rank, stats
}

// TrustRank runs PageRank with a teleport vector biased toward a hand-picked
// good seed set: trust flows forward from trustworthy pages, so a page reachable
// from the seeds along links accrues trust and a page the seeds never reach does
// not. seeds are vertex ids; an empty seed set falls back to uniform teleport
// (plain PageRank). cfg.Teleport is ignored and replaced by the seed bias.
func TrustRank(g *Graph, seeds []int, cfg Config) ([]float32, Stats) {
	cfg.Teleport = seedTeleport(g.NumVertices(), seeds)
	return PageRank(g, cfg)
}

// AntiTrustRank propagates distrust backward from a known-spam seed set: a page
// that links to spam is itself suspect. It is TrustRank on the reverse graph
// seeded with the spam pages, so the returned score is high for pages close to
// spam along inbound paths. It is combined with the forward TrustRank score
// downstream to separate ham from spam (doc 07.7).
func AntiTrustRank(g *Graph, spamSeeds []int, cfg Config) ([]float32, Stats) {
	return TrustRank(g.Reverse(), spamSeeds, cfg)
}

// seedTeleport builds a teleport vector with mass spread uniformly over the
// seeds. With no seeds it returns uniform mass over all vertices.
func seedTeleport(n int, seeds []int) []float32 {
	t := make([]float32, n)
	if len(seeds) == 0 {
		uniform := float32(1) / float32(n)
		for i := range t {
			t[i] = uniform
		}
		return t
	}
	share := float32(1) / float32(len(seeds))
	for _, s := range seeds {
		t[s] += share
	}
	return t
}

func abs(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
