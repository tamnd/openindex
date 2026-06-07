package feature

import "testing"

func TestNewVectorLength(t *testing.T) {
	v := New()
	if len(v) != int(NumFeatures) {
		t.Fatalf("New length = %d, want %d", len(v), NumFeatures)
	}
	for i, x := range v {
		if x != 0 {
			t.Fatalf("New should zero the vector, position %d = %g", i, x)
		}
	}
}

func TestAssembleFillsByPosition(t *testing.T) {
	idx := Index{BM25F: 1, DenseSim: 2, Proximity: 3, ExactMatch: 4, DocLength: 5}
	doc := Document{PageRank: 6, SiteAuthority: 7, SpamScore: 8, Freshness: 9}
	beh := Behavioral{ClickRate: 10, Dwell: 11}

	v := Assemble(nil, idx, doc, beh)

	want := map[Feature]float32{
		BM25FScore: 1, DenseSim: 2, Proximity: 3, ExactMatch: 4, DocLength: 5,
		PageRank: 6, SiteAuthority: 7, SpamScore: 8, Freshness: 9,
		ClickRate: 10, Dwell: 11,
	}
	for f, w := range want {
		if v.Get(f) != w {
			t.Fatalf("feature %d = %g, want %g", f, v.Get(f), w)
		}
	}
	// Every position must be accounted for, so the model never reads a stale slot.
	if len(want) != int(NumFeatures) {
		t.Fatalf("test covers %d features but schema has %d", len(want), NumFeatures)
	}
}

func TestAssembleReusesBackingArray(t *testing.T) {
	dst := New()
	got := Assemble(dst, Index{BM25F: 1}, Document{}, Behavioral{})
	if &got[0] != &dst[0] {
		t.Fatal("Assemble should reuse a correctly sized dst, not allocate")
	}
}

func TestAssembleAllocatesOnWrongSize(t *testing.T) {
	dst := make(Vector, 3) // wrong length
	got := Assemble(dst, Index{BM25F: 1}, Document{}, Behavioral{})
	if len(got) != int(NumFeatures) {
		t.Fatalf("wrong-sized dst should be replaced, got len %d", len(got))
	}
	if got[BM25FScore] != 1 {
		t.Fatalf("reallocated vector not filled: %g", got[BM25FScore])
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	v := New()
	v.Set(SpamScore, 0.42)
	if v.Get(SpamScore) != 0.42 {
		t.Fatalf("round trip failed: %g", v.Get(SpamScore))
	}
}
