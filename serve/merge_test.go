package serve

import (
	"sort"
	"testing"

	"openindex"
)

func shard(rs ...openindex.Result) Response { return Response{Results: rs} }

// bruteMerge is the oracle: concatenate every shard, sort by the page
// convention, take k.
func bruteMerge(shards []Response, k int) []openindex.Result {
	var all []openindex.Result
	for _, s := range shards {
		all = append(all, s.Results...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		if all[i].Doc.Segment != all[j].Doc.Segment {
			return all[i].Doc.Segment < all[j].Doc.Segment
		}
		return all[i].Doc.Local < all[j].Doc.Local
	})
	if k < len(all) {
		all = all[:k]
	}
	return all
}

func TestMergeMatchesOracle(t *testing.T) {
	shards := []Response{
		shard(res(1, 9), res(4, 5), res(7, 2)),
		shard(res(2, 8), res(5, 4)),
		shard(res(3, 7), res(6, 3), res(8, 1)),
	}
	for k := 1; k <= 9; k++ {
		got := MergeTopK(shards, k)
		want := bruteMerge(shards, k)
		if len(got) != len(want) {
			t.Fatalf("k=%d: got %d results, want %d", k, len(got), len(want))
		}
		for i := range want {
			if got[i].Doc != want[i].Doc || got[i].Score != want[i].Score {
				t.Fatalf("k=%d rank %d: got %+v, want %+v", k, i, got[i], want[i])
			}
		}
	}
}

func TestMergeTieBreak(t *testing.T) {
	// Equal scores across shards must break toward the smaller id.
	shards := []Response{
		shard(res(5, 1)),
		shard(res(2, 1)),
		shard(res(9, 1)),
	}
	got := MergeTopK(shards, 3)
	want := []openindex.DocID{2, 5, 9}
	for i, w := range want {
		if got[i].Doc.Local != w {
			t.Fatalf("rank %d: got %d, want %d", i, got[i].Doc.Local, w)
		}
	}
}

func TestMergeEmptyAndZeroK(t *testing.T) {
	if got := MergeTopK(nil, 5); len(got) != 0 {
		t.Fatalf("no shards should merge to empty, got %v", got)
	}
	if got := MergeTopK([]Response{shard(res(1, 1))}, 0); got != nil {
		t.Fatalf("k<=0 should merge to nil, got %v", got)
	}
	if got := MergeTopK([]Response{shard(), shard()}, 5); len(got) != 0 {
		t.Fatalf("empty shards should merge to empty, got %v", got)
	}
}

func TestMergeKExceedsTotal(t *testing.T) {
	shards := []Response{shard(res(1, 9)), shard(res(2, 8))}
	got := MergeTopK(shards, 100)
	if len(got) != 2 {
		t.Fatalf("k beyond total should return all, got %d", len(got))
	}
}
