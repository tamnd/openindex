package forward

import (
	"fmt"
	"testing"

	"openindex"
)

func mkDoc(i int) Document {
	d := Document{
		URL:   fmt.Sprintf("https://example.com/page/%d", i),
		Title: fmt.Sprintf("Title %d", i),
		Body:  fmt.Sprintf("Body text for document number %d with some words", i),
	}
	d.Norms[openindex.FieldBody] = uint16(10 + i)
	d.Norms[openindex.FieldTitle] = uint16(2)
	return d
}

func TestForwardRoundTrip(t *testing.T) {
	w := NewWriter(Identity{})
	const n = 50 // crosses several chunks at DefaultChunkDocs=16
	for i := range n {
		if err := w.Add(openindex.DocID(i), mkDoc(i)); err != nil {
			t.Fatal(err)
		}
	}
	s := w.Seal()
	if s.Len() != n {
		t.Fatalf("Len=%d want %d", s.Len(), n)
	}
	for i := range n {
		got, ok := s.Get(openindex.DocID(i))
		if !ok {
			t.Fatalf("doc %d missing", i)
		}
		want := mkDoc(i)
		if got != want {
			t.Fatalf("doc %d mismatch:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

// TestForwardNonZeroBase: a store may start at a non-zero id base (a segment's
// id offset), but ids are still dense from there. Out-of-range Gets miss.
func TestForwardNonZeroBase(t *testing.T) {
	w := NewWriter(Identity{})
	const base, n = 1000, 40
	for i := range n {
		if err := w.Add(openindex.DocID(base+i), mkDoc(base+i)); err != nil {
			t.Fatal(err)
		}
	}
	s := w.Seal()
	for i := range n {
		id := openindex.DocID(base + i)
		got, ok := s.Get(id)
		if !ok || got.Title != fmt.Sprintf("Title %d", id) {
			t.Fatalf("id %d: ok=%v got=%+v", id, ok, got)
		}
	}
	if _, ok := s.Get(base - 1); ok {
		t.Error("id below the base should report missing")
	}
	if _, ok := s.Get(base + n); ok {
		t.Error("id past the last stored doc should report missing")
	}
}

func TestForwardRejectsNonSequential(t *testing.T) {
	w := NewWriter(Identity{})
	if err := w.Add(10, mkDoc(10)); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(5, mkDoc(5)); err == nil {
		t.Error("a lower id after a higher one should error")
	}
	if err := w.Add(12, mkDoc(12)); err == nil {
		t.Error("a non-contiguous id (gap) should error")
	}
}
