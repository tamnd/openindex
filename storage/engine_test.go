package storage

import (
	"bytes"
	"fmt"
	"testing"
)

func collect(t *testing.T, it Iterator) [][2]string {
	t.Helper()
	defer func() { _ = it.Close() }()
	var out [][2]string
	for it.Next() {
		out = append(out, [2]string{string(it.Key()), string(it.Value())})
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iter err: %v", err)
	}
	return out
}

func TestMemEngineGetSetDelete(t *testing.T) {
	e := NewMemEngine()
	if _, ok, _ := e.Get([]byte("missing")); ok {
		t.Fatal("got hit for missing key")
	}
	mustSet(t, e, "b", "2")
	mustSet(t, e, "a", "1")
	mustSet(t, e, "a", "1b") // overwrite
	if v, ok, _ := e.Get([]byte("a")); !ok || string(v) != "1b" {
		t.Fatalf("a = %q,%v want 1b,true", v, ok)
	}
	if err := e.Delete([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.Get([]byte("a")); ok {
		t.Fatal("a still present after delete")
	}
}

func TestMemEngineScanOrdered(t *testing.T) {
	e := NewMemEngine()
	for _, k := range []string{"d", "a", "c", "b", "e"} {
		mustSet(t, e, k, k)
	}
	got := collect(t, e.Scan([]byte("b"), []byte("e")))
	want := [][2]string{{"b", "b"}, {"c", "c"}, {"d", "d"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

func TestBatchAtomicApply(t *testing.T) {
	e := NewMemEngine()
	mustSet(t, e, "keep", "old")
	var b Batch
	b.Set([]byte("x"), []byte("1"))
	b.Set([]byte("keep"), []byte("new"))
	b.Delete([]byte("absent"))
	if b.Len() != 3 {
		t.Fatalf("batch len = %d", b.Len())
	}
	if err := e.Apply(&b); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := e.Get([]byte("keep")); string(v) != "new" {
		t.Errorf("keep = %q want new", v)
	}
	if v, _, _ := e.Get([]byte("x")); string(v) != "1" {
		t.Errorf("x = %q want 1", v)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	e := NewMemEngine()
	mustSet(t, e, "k", "v1")
	snap := e.Snapshot()
	defer func() { _ = snap.Close() }()
	mustSet(t, e, "k", "v2")
	mustSet(t, e, "k2", "new")

	if v, _, _ := snap.Get([]byte("k")); string(v) != "v1" {
		t.Errorf("snapshot k = %q, want v1 (must not see later write)", v)
	}
	if _, ok, _ := snap.Get([]byte("k2")); ok {
		t.Error("snapshot saw a key written after it was taken")
	}
	if v, _, _ := e.Get([]byte("k")); string(v) != "v2" {
		t.Errorf("live k = %q, want v2", v)
	}
}

func TestPrefixEnd(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ab", "ac"},
		{"a\xff", "b"},
		{"", ""}, // no finite successor -> nil (rendered "")
	}
	for _, c := range cases {
		got := PrefixEnd([]byte(c.in))
		if string(got) != c.want {
			t.Errorf("PrefixEnd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A prefix scan must visit exactly the keys with the prefix.
	e := NewMemEngine()
	for _, k := range []string{"ab", "aba", "abz", "ac", "b"} {
		mustSet(t, e, k, "")
	}
	got := collect(t, e.Scan([]byte("ab"), PrefixEnd([]byte("ab"))))
	if len(got) != 3 {
		t.Fatalf("prefix scan got %v, want ab,aba,abz", got)
	}
}

func mustSet(t *testing.T, e Engine, k, v string) {
	t.Helper()
	if err := e.Set([]byte(k), []byte(v)); err != nil {
		t.Fatal(err)
	}
}

// returnedSlicesAreCopies guards the contract that Get/Scan hand back copies the
// caller may mutate without corrupting the store.
func TestReturnedSlicesAreCopies(t *testing.T) {
	e := NewMemEngine()
	mustSet(t, e, "k", "value")
	v, _, _ := e.Get([]byte("k"))
	for i := range v {
		v[i] = 'X'
	}
	v2, _, _ := e.Get([]byte("k"))
	if !bytes.Equal(v2, []byte("value")) {
		t.Fatalf("store corrupted by caller mutation: %q", v2)
	}
}
