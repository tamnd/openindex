package postings

import (
	"testing"

	"openindex"
)

func mkPostings(docs []openindex.DocID, freqs []uint32) []openindex.Posting {
	ps := make([]openindex.Posting, len(docs))
	for i := range docs {
		ps[i] = openindex.Posting{Doc: docs[i], Frequency: freqs[i]}
	}
	return ps
}

// collect walks a list and returns its decoded postings.
func collect(t *testing.T, l *List) []openindex.Posting {
	t.Helper()
	var got []openindex.Posting
	c := l.Cursor()
	for c.Next() {
		got = append(got, openindex.Posting{Doc: c.Doc(), Frequency: c.Freq()})
	}
	return got
}

func assertEqual(t *testing.T, got, want []openindex.Posting) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("posting %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestRoundTripSmall(t *testing.T) {
	ps := mkPostings(
		[]openindex.DocID{0, 3, 7, 100, 101, 5000},
		[]uint32{1, 2, 1, 9, 3, 1},
	)
	l, err := Encode(ps)
	if err != nil {
		t.Fatal(err)
	}
	if l.NumDocs() != len(ps) {
		t.Errorf("NumDocs=%d want %d", l.NumDocs(), len(ps))
	}
	assertEqual(t, collect(t, l), ps)
}

// TestRoundTripMultiBlock crosses several full 128-doc blocks plus a tail.
func TestRoundTripMultiBlock(t *testing.T) {
	const n = 3*BlockSize + 37
	docs := make([]openindex.DocID, n)
	freqs := make([]uint32, n)
	var d openindex.DocID
	for i := range n {
		d += openindex.DocID(1 + (i*7)%13) // irregular gaps
		docs[i] = d
		freqs[i] = uint32(1 + (i % 5))
	}
	// Inject an outlier frequency so PForDelta's exception path is exercised.
	freqs[200] = 9999
	ps := mkPostings(docs, freqs)

	l, err := Encode(ps)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, collect(t, l), ps)
}

func TestEncodeRejectsUnsorted(t *testing.T) {
	if _, err := Encode(mkPostings([]openindex.DocID{5, 3}, []uint32{1, 1})); err == nil {
		t.Error("expected error on descending doc ids")
	}
	if _, err := Encode(mkPostings([]openindex.DocID{5, 5}, []uint32{1, 1})); err == nil {
		t.Error("expected error on duplicate doc ids")
	}
}

func TestNextGEQSkipsBlocks(t *testing.T) {
	const n = 5 * BlockSize
	docs := make([]openindex.DocID, n)
	freqs := make([]uint32, n)
	for i := range n {
		docs[i] = openindex.DocID(i * 10) // 0,10,20,...
		freqs[i] = 1
	}
	l, err := Encode(mkPostings(docs, freqs))
	if err != nil {
		t.Fatal(err)
	}

	c := l.Cursor()
	// Land exactly on a present id.
	if got, ok := c.NextGEQ(2500); !ok || got != 2500 {
		t.Fatalf("NextGEQ(2500)=%d,%v want 2500,true", got, ok)
	}
	// Land on the next id above a gap.
	if got, ok := c.NextGEQ(2501); !ok || got != 2510 {
		t.Fatalf("NextGEQ(2501)=%d,%v want 2510,true", got, ok)
	}
	// Past the end.
	if _, ok := c.NextGEQ(openindex.DocID(n*10 + 1)); ok {
		t.Fatal("NextGEQ past the last doc should report exhausted")
	}
}

func TestBlockMaxMetadata(t *testing.T) {
	const n = 2 * BlockSize
	docs := make([]openindex.DocID, n)
	freqs := make([]uint32, n)
	for i := range n {
		docs[i] = openindex.DocID(i)
		freqs[i] = 1
	}
	// A big frequency in the second block must surface as that block's max.
	freqs[BlockSize+10] = 42
	l, err := Encode(mkPostings(docs, freqs))
	if err != nil {
		t.Fatal(err)
	}

	c := l.Cursor()
	c.AdvanceShallow(0)
	if c.BlockMaxFreq() != 1 {
		t.Errorf("first block max freq = %d, want 1", c.BlockMaxFreq())
	}
	c.AdvanceShallow(openindex.DocID(BlockSize + 10))
	if c.BlockMaxFreq() != 42 {
		t.Errorf("second block max freq = %d, want 42", c.BlockMaxFreq())
	}
}

func TestEmptyList(t *testing.T) {
	l, err := Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.NumDocs() != 0 {
		t.Errorf("empty list NumDocs=%d", l.NumDocs())
	}
	c := l.Cursor()
	if c.Next() {
		t.Error("empty list cursor should not advance")
	}
}
