package dedup

import (
	"math/bits"
	"strings"
	"testing"
)

func TestContentHashExactDup(t *testing.T) {
	a := ContentHash([]byte("hello world"))
	b := ContentHash([]byte("hello world"))
	c := ContentHash([]byte("hello worlds"))
	if a != b {
		t.Error("identical bodies must hash equal")
	}
	if a == c {
		t.Error("different bodies must hash differently")
	}
}

// TestSimHashStability: the same text hashes to the same fingerprint.
func TestSimHashStability(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	first, second := SimHashText(text, 2), SimHashText(text, 2)
	if first != second {
		t.Error("SimHash must be deterministic")
	}
}

// TestSimHashNearForSmallEdit: a one-sentence change to a long document should
// move only a few bits, landing inside the near-dup threshold; an unrelated
// document should be far away.
func TestSimHashNearForSmallEdit(t *testing.T) {
	base := strings.Repeat("the quick brown fox jumps over the lazy dog ", 20)
	edited := base + "one extra trailing sentence appended here"
	unrelated := strings.Repeat("completely different content about databases and storage ", 20)

	fpBase := SimHashText(base, 2)
	fpEdit := SimHashText(edited, 2)
	fpOther := SimHashText(unrelated, 2)

	if !NearDup(fpBase, fpEdit) {
		t.Errorf("small edit should be a near-dup, Hamming=%d", Hamming(fpBase, fpEdit))
	}
	if NearDup(fpBase, fpOther) {
		t.Errorf("unrelated docs should not be near-dups, Hamming=%d", Hamming(fpBase, fpOther))
	}
}

func TestHamming(t *testing.T) {
	if Hamming(0b1011, 0b1101) != 2 {
		t.Error("Hamming(1011,1101) should be 2")
	}
	if Hamming(0, ^uint64(0)) != 64 {
		t.Error("Hamming(0, all-ones) should be 64")
	}
}

// TestNearDupIndexFindsWithinThreshold checks the banded index returns a
// candidate for any fingerprint within distance 3 and nothing beyond it.
func TestNearDupIndexFindsWithinThreshold(t *testing.T) {
	idx := NewNearDupIndex()
	base := uint64(0xDEADBEEFCAFEF00D)
	idx.Add(1, base)

	// Flip exactly 3 bits across different blocks: must still be found.
	near := base ^ (1<<2 | 1<<20 | 1<<40)
	if bits.OnesCount64(base^near) != 3 {
		t.Fatalf("test setup: expected 3 bit flips")
	}
	if id, ok := idx.Query(near); !ok || id != 1 {
		t.Errorf("3-bit-away fingerprint not found: id=%d ok=%v", id, ok)
	}

	// Flip 4 bits: must not be reported as a near-dup.
	far := base ^ (1<<2 | 1<<20 | 1<<40 | 1<<60)
	if _, ok := idx.Query(far); ok {
		t.Error("4-bit-away fingerprint should not be a near-dup")
	}
}

func TestNearDupIndexAddIfNewClusters(t *testing.T) {
	idx := NewNearDupIndex()
	base := uint64(0x0123456789ABCDEF)

	id, isNew := idx.AddIfNew(100, base)
	if !isNew || id != 100 {
		t.Fatalf("first insert: id=%d isNew=%v, want 100,true", id, isNew)
	}
	// A near-dup of the first doc joins its cluster and is not new.
	near := base ^ 0b11 // 2 bits
	id, isNew = idx.AddIfNew(200, near)
	if isNew || id != 100 {
		t.Fatalf("near-dup should join cluster 100, got id=%d isNew=%v", id, isNew)
	}
	// A distant doc forms its own cluster.
	far := base ^ ^uint64(0)
	id, isNew = idx.AddIfNew(300, far)
	if !isNew || id != 300 {
		t.Fatalf("distant doc should be new cluster 300, got id=%d isNew=%v", id, isNew)
	}
	if idx.Len() != 2 {
		t.Errorf("index has %d clusters, want 2", idx.Len())
	}
}

func TestShingleOrderSensitive(t *testing.T) {
	a := Shingle("alpha beta gamma", 2)
	if _, ok := a["alpha beta"]; !ok {
		t.Errorf("expected shingle 'alpha beta' in %v", a)
	}
	// Reordered words produce different shingles, so SimHash can tell them apart.
	if SimHashText("alpha beta gamma delta", 2) == SimHashText("delta gamma beta alpha", 2) {
		t.Error("word order should change the shingled fingerprint")
	}
}
