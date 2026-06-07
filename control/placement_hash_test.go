package control

import "testing"

func TestJumpHashInRange(t *testing.T) {
	for key := range uint64(1000) {
		b := JumpHash(key, 16)
		if b < 0 || b >= 16 {
			t.Fatalf("key %d landed in bucket %d, out of [0,16)", key, b)
		}
	}
}

func TestJumpHashDeterministic(t *testing.T) {
	for key := range uint64(100) {
		first := JumpHash(key, 64)
		second := JumpHash(key, 64)
		if first != second {
			t.Fatalf("jump hash not deterministic for key %d: %d vs %d", key, first, second)
		}
	}
}

func TestJumpHashMinimalMovementOnGrowth(t *testing.T) {
	// The defining property: growing the bucket count from n to n+1 moves only a
	// small fraction of keys, near 1/(n+1), and a key that moves only ever moves
	// to the new bucket. We check the movement stays well under a loose ceiling.
	const n, keys = 100, 100000
	moved := 0
	for key := range uint64(keys) {
		before := JumpHash(key, n)
		after := JumpHash(key, n+1)
		if before != after {
			moved++
			if after != n {
				t.Fatalf("key %d moved to bucket %d, not the new bucket %d", key, after, n)
			}
		}
	}
	// Expected share is about 1/101 ~ 0.0099; allow generous slack.
	frac := float64(moved) / float64(keys)
	if frac > 0.02 {
		t.Fatalf("grew by one bucket but moved %.4f of keys, expected near %.4f", frac, 1.0/float64(n+1))
	}
}

func TestJumpHashEvenSpread(t *testing.T) {
	const buckets, keys = 10, 100000
	counts := make([]int, buckets)
	for key := range uint64(keys) {
		counts[JumpHash(key, buckets)]++
	}
	expect := float64(keys) / float64(buckets)
	for b, c := range counts {
		dev := float64(c)/expect - 1
		if dev < -0.05 || dev > 0.05 {
			t.Fatalf("bucket %d holds %d keys, %.1f%% off the even share", b, c, dev*100)
		}
	}
}

func TestPlacePicksReplicas(t *testing.T) {
	candidates := []NodeID{"n1", "n2", "n3", "n4", "n5"}
	got := Place(7, candidates, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 replicas, got %d", len(got))
	}
	// No duplicate nodes.
	seen := map[NodeID]bool{}
	for _, n := range got {
		if seen[n] {
			t.Fatalf("node %s placed twice", n)
		}
		seen[n] = true
	}
}

func TestPlaceDeterministic(t *testing.T) {
	candidates := []NodeID{"n1", "n2", "n3", "n4"}
	a := Place(42, candidates, 2)
	b := Place(42, candidates, 2)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("placement not deterministic: %v vs %v", a, b)
		}
	}
}

func TestPlaceRebalancesEvenlyOnNodeLoss(t *testing.T) {
	// Remove one node from the candidate set and confirm the shards it held
	// spread across the others rather than piling on one. We place one replica
	// per shard over many shards, drop a node, and check the displaced shards
	// land roughly evenly.
	full := []NodeID{"n1", "n2", "n3", "n4", "n5"}
	const shards = 50000
	primaryFull := make([]NodeID, shards)
	for s := range shards {
		primaryFull[s] = Place(ShardID(s), full, 1)[0]
	}

	reduced := []NodeID{"n1", "n2", "n3", "n4"} // n5 removed
	landed := map[NodeID]int{}
	var displaced int
	for s := range shards {
		if primaryFull[s] != "n5" {
			// A shard not on the removed node must not move.
			if got := Place(ShardID(s), reduced, 1)[0]; got != primaryFull[s] {
				t.Fatalf("shard %d moved though its node survived: %s -> %s", s, primaryFull[s], got)
			}
			continue
		}
		displaced++
		landed[Place(ShardID(s), reduced, 1)[0]]++
	}
	if displaced == 0 {
		t.Fatal("no shards were on the removed node, test is vacuous")
	}
	// Each of the four survivors should absorb roughly a quarter of the
	// displaced shards.
	expect := float64(displaced) / 4
	for _, n := range reduced {
		dev := float64(landed[n])/expect - 1
		if dev < -0.1 || dev > 0.1 {
			t.Fatalf("survivor %s absorbed %d of %d displaced, %.1f%% off even", n, landed[n], displaced, dev*100)
		}
	}
}

func TestPlaceEdgeCases(t *testing.T) {
	if got := Place(1, nil, 3); got != nil {
		t.Fatalf("no candidates should place nothing, got %v", got)
	}
	if got := Place(1, []NodeID{"n1"}, 0); got != nil {
		t.Fatalf("zero replicas should place nothing, got %v", got)
	}
	if got := Place(1, []NodeID{"n1", "n2"}, 9); len(got) != 2 {
		t.Fatalf("replicas beyond the candidate count should return all, got %v", got)
	}
}
