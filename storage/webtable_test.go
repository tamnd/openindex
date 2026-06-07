package storage

import (
	"bytes"
	"sort"
	"testing"
)

func TestReverseHost(t *testing.T) {
	cases := map[string]string{
		"www.example.com": "com.example.www",
		"example.com":     "com.example",
		"a.b.c.d":         "d.c.b.a",
		"EXAMPLE.com.":    "com.example",
		"":                "",
		"localhost":       "localhost",
	}
	for in, want := range cases {
		if got := ReverseHost(in); got != want {
			t.Errorf("ReverseHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDomainLocality is the property the whole row-key design exists for: every
// page of a site, and every site of a domain, sorts contiguously.
func TestDomainLocality(t *testing.T) {
	rows := [][]byte{
		RowKey("blog.example.com", "/post"),
		RowKey("www.example.com", "/a"),
		RowKey("www.example.com", "/b"),
		RowKey("shop.other.com", "/"),
		RowKey("www.example.org", "/"),
	}
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = string(r)
	}
	sort.Strings(keys)
	// All example.com hosts share the "com.example" prefix and so are adjacent.
	first, last := -1, -1
	for i, k := range keys {
		if bytes.HasPrefix([]byte(k), []byte("com.example.")) {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 || last-first+1 != 3 {
		t.Fatalf("example.com rows not contiguous: sorted=%v", keys)
	}
}

func TestOrderedEncodingRoundTrip(t *testing.T) {
	for _, seg := range [][]byte{
		[]byte("plain"),
		{0x00},
		{0x00, 0x00},
		{0x01, 0x00, 0x02},
		{},
		[]byte("/path/with\x00null"),
	} {
		enc := appendOrdered(nil, seg)
		got := decodeOrdered(enc)
		if !bytes.Equal(got, seg) {
			t.Errorf("round trip %v -> %v -> %v", seg, enc, got)
		}
	}
}

// TestOrderedEncodingPreservesOrder checks that the framing keeps lexicographic
// order, including the prefix-sorts-first case framing must not break.
func TestOrderedEncodingPreservesOrder(t *testing.T) {
	segs := [][]byte{[]byte("ab"), []byte("a"), []byte("abc"), {0x00}, []byte("b")}
	enc := make([]string, len(segs))
	for i, s := range segs {
		enc[i] = string(appendOrdered(nil, s))
	}
	raw := make([]string, len(segs))
	for i, s := range segs {
		raw[i] = string(s)
	}
	sort.Strings(enc)
	sort.Strings(raw)
	// Decode the sorted encodings and compare to the sorted raw segments.
	for i := range enc {
		if string(decodeOrdered([]byte(enc[i]))) != raw[i] {
			t.Fatalf("order mismatch at %d: enc-order=%q raw-order=%q", i,
				decodeOrdered([]byte(enc[i])), raw[i])
		}
	}
}

func TestWebTableVersionsNewestFirst(t *testing.T) {
	wt := NewWebTable(NewMemEngine())
	row := RowKey("www.example.com", "/page")
	for _, v := range []struct {
		ts   int64
		body string
	}{{100, "v100"}, {300, "v300"}, {200, "v200"}} {
		if err := wt.Put(row, FamilyContents, nil, v.ts, []byte(v.body)); err != nil {
			t.Fatal(err)
		}
	}
	cell, ok, err := wt.Latest(row, FamilyContents, nil)
	if err != nil || !ok {
		t.Fatalf("Latest ok=%v err=%v", ok, err)
	}
	if string(cell.Value) != "v300" || cell.TSNanos != 300 {
		t.Fatalf("latest = %q@%d, want v300@300", cell.Value, cell.TSNanos)
	}
}

func TestWebTableScanFamilyCollapsesVersions(t *testing.T) {
	wt := NewWebTable(NewMemEngine())
	row := RowKey("www.example.com", "/p")
	// Two qualifiers in the meta family, each with two versions.
	_ = wt.Put(row, FamilyMeta, []byte("lang"), 100, []byte("en-old"))
	_ = wt.Put(row, FamilyMeta, []byte("lang"), 200, []byte("en"))
	_ = wt.Put(row, FamilyMeta, []byte("status"), 150, []byte("200"))

	cells, err := wt.ScanFamily(row, FamilyMeta)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2 (one per qualifier): %+v", len(cells), cells)
	}
	got := map[string]string{}
	for _, c := range cells {
		got[string(c.Qualifier)] = string(c.Value)
	}
	if got["lang"] != "en" || got["status"] != "200" {
		t.Fatalf("collapsed cells = %v, want newest of each", got)
	}
}

func TestWebTableGCVersions(t *testing.T) {
	wt := NewWebTable(NewMemEngine())
	row := RowKey("www.example.com", "/p")
	for ts := int64(1); ts <= 5; ts++ {
		_ = wt.Put(row, FamilyContents, nil, ts*100, []byte{byte(ts)})
	}
	if err := wt.GCVersions(row, FamilyContents, 2); err != nil {
		t.Fatal(err)
	}
	prefix := cellKeyQualPrefix(row, FamilyContents, nil)
	n := len(collect(t, wt.eng.Scan(prefix, PrefixEnd(prefix))))
	if n != 2 {
		t.Fatalf("after GC keep=2 there are %d versions, want 2", n)
	}
	// The two survivors must be the newest.
	cell, _, _ := wt.Latest(row, FamilyContents, nil)
	if cell.TSNanos != 500 {
		t.Fatalf("newest survivor ts = %d, want 500", cell.TSNanos)
	}
}

func TestDecodeQualifier(t *testing.T) {
	row := RowKey("www.example.com", "/p")
	key := cellKey(row, FamilyMeta, []byte("lang"), 123)
	if q := decodeQualifier(key, row); string(q) != "lang" {
		t.Fatalf("decodeQualifier = %q, want lang", q)
	}
	if ts := decodeTS(key); ts != 123 {
		t.Fatalf("decodeTS = %d, want 123", ts)
	}
}
