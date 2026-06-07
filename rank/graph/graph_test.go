package graph

import (
	"math"
	"testing"
)

// build constructs a graph from an edge list.
func build(n int, edges [][2]int) *Graph {
	g := New(n)
	for _, e := range edges {
		g.AddEdge(e[0], e[1])
	}
	return g
}

func sum(v []float32) float32 {
	var s float32
	for _, x := range v {
		s += x
	}
	return s
}

func TestPageRankConservesMass(t *testing.T) {
	g := build(4, [][2]int{{0, 1}, {0, 2}, {1, 2}, {2, 0}, {3, 2}})
	pr, stats := PageRank(g, Config{})
	if !stats.Converged {
		t.Fatalf("expected convergence, stats=%+v", stats)
	}
	if math.Abs(float64(sum(pr))-1) > 1e-3 {
		t.Fatalf("rank should sum to ~1, got %g", sum(pr))
	}
}

func TestPageRankRewardsInlinks(t *testing.T) {
	// A star: 1, 2, 3 all link to 0. Vertex 0 should hold the most rank.
	g := build(4, [][2]int{{1, 0}, {2, 0}, {3, 0}})
	pr, _ := PageRank(g, Config{})
	for v := 1; v < 4; v++ {
		if pr[0] <= pr[v] {
			t.Fatalf("hub 0 (%g) should outrank spoke %d (%g)", pr[0], v, pr[v])
		}
	}
}

func TestPageRankSymmetryGivesEqualRanks(t *testing.T) {
	// Two vertices linking to each other are interchangeable.
	g := build(2, [][2]int{{0, 1}, {1, 0}})
	pr, _ := PageRank(g, Config{})
	if math.Abs(float64(pr[0]-pr[1])) > 1e-5 {
		t.Fatalf("symmetric pair should have equal rank: %g vs %g", pr[0], pr[1])
	}
}

func TestPageRankDanglingNodeNoLeak(t *testing.T) {
	// Vertex 2 has no out-edges. Its mass must be redistributed, not lost.
	g := build(3, [][2]int{{0, 1}, {1, 2}})
	pr, stats := PageRank(g, Config{})
	if !stats.Converged {
		t.Fatalf("expected convergence with a dangling node, stats=%+v", stats)
	}
	if math.Abs(float64(sum(pr))-1) > 1e-3 {
		t.Fatalf("dangling mass leaked: rank sums to %g, want ~1", sum(pr))
	}
}

func TestPageRankDeterministic(t *testing.T) {
	g := build(5, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 0}, {0, 2}})
	a, _ := PageRank(g, Config{})
	b, _ := PageRank(g, Config{})
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %g vs %g", i, a[i], b[i])
		}
	}
}

func TestTrustRankBiasesTowardSeeds(t *testing.T) {
	// Two disconnected components: {0->1} trusted, {2->3} untrusted.
	g := build(4, [][2]int{{0, 1}, {1, 0}, {2, 3}, {3, 2}})
	pr, _ := TrustRank(g, []int{0}, Config{})
	trustedSide := pr[0] + pr[1]
	untrustedSide := pr[2] + pr[3]
	if trustedSide <= untrustedSide {
		t.Fatalf("seeded component (%g) should hold more trust than the unseeded one (%g)", trustedSide, untrustedSide)
	}
}

func TestAntiTrustRankPropagatesBackward(t *testing.T) {
	// 0 -> 1 -> 2(spam). Distrust should flow back to 0 and 1, the pages that
	// reach spam, more than to a page spam cannot reach.
	g := build(4, [][2]int{{0, 1}, {1, 2}, {3, 3}})
	score, _ := AntiTrustRank(g, []int{2}, Config{})
	if score[1] <= score[3] {
		t.Fatalf("page linking toward spam (%g) should carry more distrust than an isolated page (%g)", score[1], score[3])
	}
}

func TestReverse(t *testing.T) {
	g := build(3, [][2]int{{0, 1}, {0, 2}})
	r := g.Reverse()
	if r.OutDegree(1) != 1 || r.OutDegree(2) != 1 || r.OutDegree(0) != 0 {
		t.Fatalf("reverse degrees wrong: %d %d %d", r.OutDegree(0), r.OutDegree(1), r.OutDegree(2))
	}
}

func TestEmptyGraph(t *testing.T) {
	pr, stats := PageRank(New(0), Config{})
	if pr != nil || !stats.Converged {
		t.Fatalf("empty graph should return nil, converged; got %v %+v", pr, stats)
	}
}

func TestMaxSupersteps(t *testing.T) {
	// An asymmetric graph whose rank vector is still moving, plus a tolerance it
	// cannot meet, forces the cap to terminate the run.
	g := build(4, [][2]int{{0, 1}, {0, 2}, {1, 2}, {2, 0}, {3, 2}})
	_, stats := PageRank(g, Config{Tolerance: 1e-30, MaxSupersteps: 2})
	if stats.Converged || stats.Supersteps != 2 {
		t.Fatalf("should stop at the cap unconverged, got %+v", stats)
	}
}
