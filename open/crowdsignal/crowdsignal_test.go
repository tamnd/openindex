package crowdsignal

import (
	"math"
	"math/rand/v2"
	"testing"

	"openindex"
)

func doc(seg, local uint32) openindex.GlobalDocID {
	return openindex.GlobalDocID{Segment: openindex.SegmentID(seg), Local: openindex.DocID(local)}
}

func TestSquashCollapsesRepeats(t *testing.T) {
	d := doc(1, 7)
	session := []Report{
		{Query: "cats", Doc: d, Outcome: Outcome{Good: true}},
		{Query: "cats", Doc: d, Outcome: Outcome{LastLongest: true}},
		{Query: "cats", Doc: d, Outcome: Outcome{Good: true}},
		{Query: "dogs", Doc: d, Outcome: Outcome{Pogo: true}},
	}
	got := Squash(session)
	if len(got) != 2 {
		t.Fatalf("two distinct (query, doc) pairs should survive squashing, got %d", len(got))
	}
	// The three "cats" reports merge into one with the bits OR-ed.
	var cats Report
	for _, r := range got {
		if r.Query == "cats" {
			cats = r
		}
	}
	if !cats.Outcome.Good || !cats.Outcome.LastLongest || cats.Outcome.Pogo {
		t.Fatalf("squashed cats outcome wrong: %+v", cats.Outcome)
	}
}

func TestKeepProbAboveHalf(t *testing.T) {
	// The aggregate is only recoverable when truth is reported more often than
	// noise, so keepProb must exceed one half for any positive epsilon.
	for _, eps := range []float64{0.5, 1, 2, 5} {
		if p := keepProb(eps); p <= 0.5 {
			t.Fatalf("keepProb(%v) = %v, must exceed 0.5", eps, p)
		}
	}
}

func TestAggregateRecoversTruthAtScale(t *testing.T) {
	// The core privacy claim: each report is noised on the client, yet the
	// de-biased aggregate recovers the true counts in expectation. Drive many
	// clients through Privatize and Observe and check the estimate lands close to
	// the planted truth.
	rng := rand.New(rand.NewPCG(42, 99))
	const eps = 1.0
	agg := NewAggregator(eps)
	d := doc(2, 3)

	const n = 20000
	trueGood := 0
	for i := range n {
		// 70 percent of impressions are good clicks; none are pogo.
		good := i%10 < 7
		if good {
			trueGood++
		}
		r := Report{Query: "weather", Doc: d, Outcome: Outcome{Good: good}}
		agg.Observe(Privatize(r, eps, rng))
	}

	s := agg.Stats("weather", d)
	if s.Reports != n {
		t.Fatalf("expected %d reports, got %d", n, s.Reports)
	}
	// The estimate should be within a few percent of the planted 14000 good
	// clicks despite every single report being independently noised.
	if rel := math.Abs(s.GoodClicks-float64(trueGood)) / float64(trueGood); rel > 0.05 {
		t.Fatalf("good-click estimate %v off true %d by %.1f%%", s.GoodClicks, trueGood, rel*100)
	}
	// Pogo was never true, so its estimate should sit near zero.
	if s.Pogo > float64(n)*0.05 {
		t.Fatalf("pogo estimate %v should be near zero", s.Pogo)
	}
}

func TestStatsUnknownKeyIsZero(t *testing.T) {
	agg := NewAggregator(1.0)
	if s := agg.Stats("never seen", doc(0, 0)); s != (Stats{}) {
		t.Fatalf("an unseen key should return zero stats, got %+v", s)
	}
}

func TestPrivatizeIsDeterministicWithSeededRNG(t *testing.T) {
	// Same seed, same input, same privatized output: the noise is reproducible for
	// testing even though it is random in production.
	r := Report{Query: "q", Doc: doc(1, 1), Outcome: Outcome{Good: true, Pogo: true}}
	a := Privatize(r, 1.0, rand.New(rand.NewPCG(7, 7)))
	b := Privatize(r, 1.0, rand.New(rand.NewPCG(7, 7)))
	if a != b {
		t.Fatalf("seeded privatization should be reproducible: %+v vs %+v", a, b)
	}
}
