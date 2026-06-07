package ciff

import (
	"bytes"
	"reflect"
	"testing"
)

func sampleSource() *MemSource {
	terms := []PostingsList{
		// Deliberately out of order so NewMemSource has to sort.
		{Term: "search", DF: 2, CF: 5, Postings: []Posting{{DocID: 2, TF: 2}, {DocID: 0, TF: 3}}},
		{Term: "engine", DF: 1, CF: 1, Postings: []Posting{{DocID: 1, TF: 1}}},
		{Term: "open", DF: 3, CF: 6, Postings: []Posting{{DocID: 0, TF: 1}, {DocID: 1, TF: 2}, {DocID: 2, TF: 3}}},
	}
	docs := []DocRecord{
		{DocID: 2, CollectionDocID: "https://c.example", Length: 8},
		{DocID: 0, CollectionDocID: "https://a.example", Length: 4},
		{DocID: 1, CollectionDocID: "https://b.example", Length: 3},
	}
	return NewMemSource("sample export", terms, docs)
}

func TestWriteReadRoundTrip(t *testing.T) {
	src := sampleSource()
	var buf bytes.Buffer
	if err := Write(&buf, src); err != nil {
		t.Fatal(err)
	}
	idx, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if idx.Header.NumPostingsLists != 3 || idx.Header.NumDocs != 3 {
		t.Fatalf("header counts wrong: %+v", idx.Header)
	}
	if idx.Header.TotalTerms != 15 {
		t.Fatalf("total terms should be 4+3+8=15, got %d", idx.Header.TotalTerms)
	}
	if idx.Header.AverageDocLength != 5 {
		t.Fatalf("average doc length should be 15/3=5, got %v", idx.Header.AverageDocLength)
	}

	// Terms come back in lexicographic order with postings ascending by docid.
	wantTerms := []string{"engine", "open", "search"}
	for i, w := range wantTerms {
		if idx.Terms[i].Term != w {
			t.Fatalf("term %d should be %q, got %q", i, w, idx.Terms[i].Term)
		}
	}
	search := idx.Terms[2]
	if !reflect.DeepEqual(search.Postings, []Posting{{DocID: 0, TF: 3}, {DocID: 2, TF: 2}}) {
		t.Fatalf("search postings did not round-trip sorted: %+v", search.Postings)
	}

	// Docs come back in ascending internal id with their external ids intact.
	if idx.Docs[0].CollectionDocID != "https://a.example" || idx.Docs[2].CollectionDocID != "https://c.example" {
		t.Fatalf("doc records did not round-trip in order: %+v", idx.Docs)
	}
}

func TestWriteIsDeterministic(t *testing.T) {
	// The same source must produce byte-identical output, or the export would not
	// content-address stably and two operators could never confirm a match.
	var a, b bytes.Buffer
	if err := Write(&a, sampleSource()); err != nil {
		t.Fatal(err)
	}
	if err := Write(&b, sampleSource()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("the same source produced different bytes")
	}
}

func TestGapEncodingHandlesWideDocIDs(t *testing.T) {
	// A list with large, sparse doc ids must still round-trip; the gap encoding is
	// where an off-by-one in the delta math would show up.
	terms := []PostingsList{{
		Term: "sparse", DF: 3, CF: 3,
		Postings: []Posting{{DocID: 5, TF: 1}, {DocID: 1000, TF: 1}, {DocID: 1<<20 + 7, TF: 1}},
	}}
	docs := []DocRecord{{DocID: 0, CollectionDocID: "d", Length: 1}}
	var buf bytes.Buffer
	if err := Write(&buf, NewMemSource("", terms, docs)); err != nil {
		t.Fatal(err)
	}
	idx, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := idx.Terms[0].Postings
	want := []Posting{{DocID: 5, TF: 1}, {DocID: 1000, TF: 1}, {DocID: 1<<20 + 7, TF: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wide doc ids did not round-trip: %+v", got)
	}
}

func TestReadRejectsBadMagic(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte("NOPEthen some bytes"))); err == nil {
		t.Fatal("a stream with the wrong magic should be rejected")
	}
}

func TestEmptyIndexRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, NewMemSource("empty", nil, nil)); err != nil {
		t.Fatal(err)
	}
	idx, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Header.NumPostingsLists != 0 || idx.Header.NumDocs != 0 || idx.Header.AverageDocLength != 0 {
		t.Fatalf("empty index header wrong: %+v", idx.Header)
	}
}
