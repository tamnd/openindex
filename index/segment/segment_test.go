package segment

import (
	"testing"

	"openindex"
	"openindex/index/forward"
)

// doc builds a forward document plus its term-frequency map from a word list.
func doc(url string, words ...string) (forward.Document, map[string]uint32) {
	tf := map[string]uint32{}
	for _, w := range words {
		tf[w]++
	}
	return forward.Document{URL: url, Title: url, Body: url}, tf
}

func buildSeg(t *testing.T, id openindex.SegmentID) *Segment {
	t.Helper()
	b := NewBuilder(id)
	d0, tf0 := doc("u0", "apple", "banana", "apple")
	d1, tf1 := doc("u1", "banana", "cherry")
	d2, tf2 := doc("u2", "cherry") // singleton term "cherry" appears in d1,d2 -> df 2
	b.AddDocument(d0, tf0)
	b.AddDocument(d1, tf1)
	b.AddDocument(d2, tf2)
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSegmentBuildAndQuery(t *testing.T) {
	s := buildSeg(t, 1)
	if s.NumDocs() != 3 || s.LiveDocs() != 3 {
		t.Fatalf("NumDocs=%d LiveDocs=%d want 3,3", s.NumDocs(), s.LiveDocs())
	}

	// "apple" occurs only in doc 0, with frequency 2.
	c, df, ok := s.Postings("apple")
	if !ok || df != 1 {
		t.Fatalf("apple postings ok=%v df=%d want true,1", ok, df)
	}
	if !c.Next() || c.Doc() != 0 || c.Freq() != 2 {
		t.Fatalf("apple posting wrong: doc=%d freq=%d", c.Doc(), c.Freq())
	}

	// "banana" occurs in docs 0 and 1.
	c, df, _ = s.Postings("banana")
	if df != 2 {
		t.Errorf("banana df=%d want 2", df)
	}
	var docs []openindex.DocID
	for c.Next() {
		docs = append(docs, c.Doc())
	}
	if len(docs) != 2 || docs[0] != 0 || docs[1] != 1 {
		t.Errorf("banana docs=%v want [0 1]", docs)
	}

	// A missing term must report not found.
	if _, _, ok := s.Postings("durian"); ok {
		t.Error("durian should not be found")
	}
}

func TestSegmentSingletonTerm(t *testing.T) {
	b := NewBuilder(1)
	d, tf := doc("only", "unique")
	b.AddDocument(d, tf)
	s, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s.Dict().Lookup("unique")
	if !ok || !e.Singleton || e.SingletonDoc != 0 {
		t.Fatalf("unique should be a singleton on doc 0, got %+v ok=%v", e, ok)
	}
	// Postings still presents a uniform cursor for the singleton.
	c, df, _ := s.Postings("unique")
	if df != 1 || !c.Next() || c.Doc() != 0 {
		t.Errorf("singleton cursor wrong: df=%d", df)
	}
}

func TestSegmentDeleteAndForward(t *testing.T) {
	s := buildSeg(t, 1)
	if _, ok := s.Document(1); !ok {
		t.Fatal("doc 1 should be live")
	}
	if !s.Delete(1) {
		t.Fatal("deleting a live doc should report true")
	}
	if s.Delete(1) {
		t.Error("deleting an already-deleted doc should report false")
	}
	if _, ok := s.Document(1); ok {
		t.Error("deleted doc should not be retrievable")
	}
	if s.LiveDocs() != 2 {
		t.Errorf("LiveDocs=%d want 2 after one delete", s.LiveDocs())
	}
}

// TestMergeReclaimsDeletes: merging two segments, one with a deleted doc,
// produces a compact segment with fresh dense ids and no deleted documents.
func TestMergeReclaimsDeletes(t *testing.T) {
	a := buildSeg(t, 1)
	b := buildSeg(t, 2)
	a.Delete(0) // drop doc 0 of the first segment, which contains "banana"

	merged, err := Merge(99, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if merged.ID() != 99 {
		t.Errorf("merged id=%d want 99", merged.ID())
	}
	// a contributed 2 live docs, b contributed 3, all live in the result.
	if merged.NumDocs() != 5 || merged.LiveDocs() != 5 {
		t.Fatalf("merged NumDocs=%d LiveDocs=%d want 5,5", merged.NumDocs(), merged.LiveDocs())
	}
	// "banana" appeared in 2 docs of a (doc 0 deleted -> 1 survives) + 2 of b = 3.
	_, df, ok := merged.Postings("banana")
	if !ok || df != 3 {
		t.Errorf("merged banana df=%d ok=%v want 3,true", df, ok)
	}
	// Doc ids are reassigned densely from 0.
	for i := range merged.NumDocs() {
		if _, ok := merged.Document(openindex.DocID(i)); !ok {
			t.Errorf("merged doc %d should be live and retrievable", i)
		}
	}
}

func TestMergePolicySelect(t *testing.T) {
	p := DefaultMergePolicy()
	// Fewer than SegmentsPerTier and no deletes: nothing to do.
	few := []*Segment{buildSeg(t, 1), buildSeg(t, 2)}
	if got := p.Select(few); got != nil {
		t.Errorf("below tier threshold should select nothing, got %d", len(got))
	}
	// A segment over the delete threshold is selected on its own.
	s := buildSeg(t, 3)
	s.Delete(0)
	s.Delete(1) // 2 of 3 deleted = 66% > 33%
	if got := p.Select([]*Segment{s}); len(got) != 1 || got[0] != s {
		t.Errorf("over-deletes segment should be selected for rewrite, got %v", got)
	}
}
