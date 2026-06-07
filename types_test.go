package openindex

import (
	"sort"
	"testing"
)

func TestContentHashString(t *testing.T) {
	var h ContentHash
	h[0] = 0xde
	h[1] = 0xad
	h[31] = 0xff
	got := h.String()
	if len(got) != 64 {
		t.Fatalf("hex length = %d, want 64", len(got))
	}
	if got[:4] != "dead" {
		t.Errorf("prefix = %q, want %q", got[:4], "dead")
	}
	if got[62:] != "ff" {
		t.Errorf("suffix = %q, want %q", got[62:], "ff")
	}
}

func TestScoreOrdersDescending(t *testing.T) {
	scores := []Score{0.2, 0.9, 0.5, 0.1}
	sort.Slice(scores, func(i, j int) bool { return scores[i].Less(scores[j]) })
	want := []Score{0.9, 0.5, 0.2, 0.1}
	for i := range want {
		if scores[i] != want[i] {
			t.Fatalf("sorted = %v, want %v", scores, want)
		}
	}
}

func TestFieldString(t *testing.T) {
	cases := map[Field]string{
		FieldBody:   "body",
		FieldTitle:  "title",
		FieldAnchor: "anchor",
		FieldURL:    "url",
	}
	for f, want := range cases {
		if got := f.String(); got != want {
			t.Errorf("Field(%d).String() = %q, want %q", f, got, want)
		}
	}
	if NumFields != 4 {
		t.Errorf("NumFields = %d, want 4", NumFields)
	}
}
