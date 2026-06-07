package storage

import (
	"sort"
	"testing"
)

func TestLinkGraphForwardAndBack(t *testing.T) {
	g := NewLinkGraph(NewMemEngine())
	a := RowKey("a.com", "/")
	b := RowKey("b.com", "/")
	c := RowKey("c.com", "/")

	mustLink(t, g, a, b, "to b")
	mustLink(t, g, a, c, "to c")
	mustLink(t, g, c, b, "c says b")

	out, err := g.OutLinks(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("a out-degree = %d, want 2", len(out))
	}

	// Link inversion: b's inbound edges carry the anchor text from a and c.
	in, err := g.InLinks(b)
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]string{}
	for _, e := range in {
		anchors[string(e.Node)] = e.Anchor
	}
	if len(anchors) != 2 || anchors[string(a)] != "to b" || anchors[string(c)] != "c says b" {
		t.Fatalf("inbound anchors for b = %v", anchors)
	}
}

func TestLinkGraphOutDegree(t *testing.T) {
	g := NewLinkGraph(NewMemEngine())
	src := RowKey("hub.com", "/")
	for _, h := range []string{"x.com", "y.com", "z.com"} {
		mustLink(t, g, src, RowKey(h, "/"), "")
	}
	n, err := g.OutDegree(src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("OutDegree = %d, want 3", n)
	}
}

// TestInLinksNodeOrder confirms inbound sources come back in node-key order,
// which keeps the link-inversion output deterministic.
func TestInLinksNodeOrder(t *testing.T) {
	g := NewLinkGraph(NewMemEngine())
	dst := RowKey("target.com", "/")
	srcs := []string{"m.com", "a.com", "z.com"}
	for _, h := range srcs {
		mustLink(t, g, RowKey(h, "/"), dst, "")
	}
	in, err := g.InLinks(dst)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(in))
	for i, e := range in {
		got[i] = string(e.Node)
	}
	want := make([]string, len(in))
	copy(want, got)
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("InLinks not in node order: %v", got)
		}
	}
}

func mustLink(t *testing.T, g *LinkGraph, src, dst []byte, anchor string) {
	t.Helper()
	if err := g.AddLink(src, dst, anchor); err != nil {
		t.Fatal(err)
	}
}
