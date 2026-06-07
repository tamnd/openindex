package terms

import "testing"

func build() *SortedDict {
	return NewSortedDict([]Term{
		{Term: "banana", Entry: Entry{DocFreq: 3, PostingOffset: 100}},
		{Term: "apple", Entry: Entry{DocFreq: 1, Singleton: true, SingletonDoc: 7}},
		{Term: "apricot", Entry: Entry{DocFreq: 2, PostingOffset: 50}},
		{Term: "cherry", Entry: Entry{DocFreq: 5, PostingOffset: 200}},
		{Term: "app", Entry: Entry{DocFreq: 9, PostingOffset: 10}},
	})
}

func TestLookupHitAndMiss(t *testing.T) {
	d := build()
	e, ok := d.Lookup("banana")
	if !ok || e.DocFreq != 3 || e.PostingOffset != 100 {
		t.Errorf("banana lookup wrong: %+v ok=%v", e, ok)
	}
	if _, ok := d.Lookup("durian"); ok {
		t.Error("durian should miss")
	}
	if _, ok := d.Lookup("appl"); ok {
		t.Error("partial term appl should miss (exact lookup only)")
	}
}

func TestSingletonEntry(t *testing.T) {
	d := build()
	e, ok := d.Lookup("apple")
	if !ok || !e.Singleton || e.SingletonDoc != 7 {
		t.Errorf("apple should be a singleton on doc 7, got %+v", e)
	}
}

func TestPrefixScanIsOrderedAndBounded(t *testing.T) {
	d := build()
	var got []string
	d.PrefixScan("ap", func(term string, _ Entry) bool {
		got = append(got, term)
		return true
	})
	// "app", "apple", "apricot" share the prefix; "banana" must not appear.
	want := []string{"app", "apple", "apricot"}
	if len(got) != len(want) {
		t.Fatalf("prefix scan got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("prefix scan order wrong at %d: got %v want %v", i, got, want)
		}
	}
}

func TestPrefixScanEarlyStop(t *testing.T) {
	d := build()
	var count int
	d.PrefixScan("a", func(_ string, _ Entry) bool {
		count++
		return count < 2 // stop after two
	})
	if count != 2 {
		t.Errorf("early stop should visit exactly 2 terms, visited %d", count)
	}
}

func TestDuplicateKeepsLastWrite(t *testing.T) {
	d := NewSortedDict([]Term{
		{Term: "x", Entry: Entry{DocFreq: 1}},
		{Term: "x", Entry: Entry{DocFreq: 9}},
	})
	if d.Len() != 1 {
		t.Fatalf("duplicate term should collapse to one, len=%d", d.Len())
	}
	e, _ := d.Lookup("x")
	if e.DocFreq != 9 {
		t.Errorf("duplicate should keep last write, got DocFreq=%d", e.DocFreq)
	}
}

var _ Dictionary = (*SortedDict)(nil)
