package frontier

import (
	"testing"
	"time"
)

// testClock is a hand-advanced clock so politeness gaps are deterministic.
type testClock struct{ t time.Time }

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)}
}
func (c *testClock) now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestSeenSetDeduplicates(t *testing.T) {
	f := New(NewMemSeenSet())
	u := URL{Raw: "https://a.com/x", Host: "a.com", Fingerprint: 1}
	if !f.Add(u) {
		t.Fatal("first Add should admit")
	}
	if f.Add(u) {
		t.Error("second Add of same fingerprint should be rejected")
	}
	if f.Len() != 1 {
		t.Errorf("Len = %d, want 1", f.Len())
	}
}

func TestPriorityOrder(t *testing.T) {
	clk := newTestClock()
	f := New(NewMemSeenSet(), WithClock(clk.now))
	// Three different hosts so politeness does not interfere; distinct priorities.
	f.Add(URL{Raw: "lo", Host: "lo.com", Priority: 0, Fingerprint: 1})
	f.Add(URL{Raw: "hi", Host: "hi.com", Priority: 7, Fingerprint: 2})
	f.Add(URL{Raw: "mid", Host: "mid.com", Priority: 3, Fingerprint: 3})

	got := make([]string, 0, 3)
	for range 3 {
		u, ok := f.Next()
		if !ok {
			t.Fatalf("expected a ready URL, got none at step %d", len(got))
		}
		got = append(got, u.Raw)
		f.Done(u, time.Millisecond)
	}
	if got[0] != "hi" || got[1] != "mid" || got[2] != "lo" {
		t.Errorf("priority order wrong: %v", got)
	}
}

func TestHostPolitenessBackoff(t *testing.T) {
	clk := newTestClock()
	f := New(NewMemSeenSet(), WithClock(clk.now), WithPolitenessFactor(10))

	f.Add(URL{Raw: "p1", Host: "x.com", Fingerprint: 1})
	f.Add(URL{Raw: "p2", Host: "x.com", Fingerprint: 2})

	// First page is ready immediately.
	u1, ok := f.Next()
	if !ok {
		t.Fatal("first page should be ready")
	}
	// The fetch took 1s, so the next gap is 10 * 1s = 10s.
	f.Done(u1, time.Second)

	// Before the gap elapses the same host yields nothing.
	clk.advance(9 * time.Second)
	if _, ok := f.Next(); ok {
		t.Error("host should still be in its politeness gap")
	}
	// After the gap the second page becomes available.
	clk.advance(2 * time.Second)
	u2, ok := f.Next()
	if !ok {
		t.Fatal("second page should be ready after the gap")
	}
	if u2.Raw != "p2" {
		t.Errorf("expected p2, got %q", u2.Raw)
	}
}

// TestSingleHostConcurrency: while a host's URL is checked out, the frontier
// must not hand out another URL for the same host.
func TestSingleHostConcurrency(t *testing.T) {
	clk := newTestClock()
	f := New(NewMemSeenSet(), WithClock(clk.now))
	f.Add(URL{Raw: "p1", Host: "x.com", Fingerprint: 1})
	f.Add(URL{Raw: "p2", Host: "x.com", Fingerprint: 2})

	u1, ok := f.Next()
	if !ok {
		t.Fatal("first page should be ready")
	}
	// p1 is checked out but not Done; the host is busy.
	if _, ok := f.Next(); ok {
		t.Error("a busy host must not yield a second URL concurrently")
	}
	f.Done(u1, time.Millisecond)
	clk.advance(time.Second)
	if _, ok := f.Next(); !ok {
		t.Error("after Done and the gap, the host should yield its next URL")
	}
}

func TestCrawlDelayRaisesFloor(t *testing.T) {
	clk := newTestClock()
	f := New(NewMemSeenSet(), WithClock(clk.now), WithPolitenessFactor(1))
	f.SetCrawlDelay("x.com", 5*time.Second)
	f.Add(URL{Raw: "p1", Host: "x.com", Fingerprint: 1})
	f.Add(URL{Raw: "p2", Host: "x.com", Fingerprint: 2})

	u1, _ := f.Next()
	// factor 1 * 1ms would be tiny, but the 5s crawl-delay is the floor.
	f.Done(u1, time.Millisecond)
	clk.advance(time.Second)
	if _, ok := f.Next(); ok {
		t.Error("crawl-delay floor of 5s should keep the host unavailable at 1s")
	}
	clk.advance(5 * time.Second)
	if _, ok := f.Next(); !ok {
		t.Error("host should be available once the crawl-delay floor passes")
	}
}
