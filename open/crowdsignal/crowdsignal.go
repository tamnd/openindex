// Package crowdsignal is the opt-in, privacy-preserving crowd signal
// (implementation doc 11.3). The leaks showed behavioral signals (good clicks,
// the last longest click, pogo-sticking) are decisive for ranking (doc 07.6), but
// collecting them the incumbent way means building a per-user profile. This
// package collects the same aggregate statistics the late re-ranker needs without
// ever holding identity, which is the proof of the trust thesis and the
// cold-start asset at once (doc 01.6).
//
// The protocol has three parts, mirroring the Brave Web Discovery design:
//
//   - Squashing on the client collapses repeated outcomes for one (query,
//     document) from a single session into one, so a user clicking the same
//     result ten times counts once. This is the unsquashed-click spam filter
//     Navboost uses (doc 07.6), done before submission so the server never sees
//     the raw multiplicity.
//   - Local differential privacy on the client adds noise to each reported bit
//     (randomized response) before it leaves the device, so the submitted record
//     is provably private and a colluding group can only shift the aggregates by a
//     bounded amount (doc 11.5).
//   - Unbiased aggregation on the server de-noises the counts in expectation, so
//     the published statistics are accurate at scale even though no single report
//     is trustworthy.
//
// The records carry no user identifier by construction; true unlinkability is the
// job of the submission transport (the Brave proxy mechanism), which this package
// assumes and does not implement.
package crowdsignal

import (
	"math"
	"math/rand/v2"

	"openindex"
)

// Outcome is the behavioral signal for one impression: whether the click was a
// good click, whether it was the last and longest click of the session (the
// strongest satisfaction signal), and whether the user pogo-sticked back to the
// results (a dissatisfaction signal). These are exactly the per-(query, document)
// signals the late re-ranker wants (doc 07.6).
type Outcome struct {
	Good        bool
	LastLongest bool
	Pogo        bool
}

// Report is one client submission: the query, the document the outcome is about,
// and the outcome. There is no identity field, by design.
type Report struct {
	Query   string
	Doc     openindex.GlobalDocID
	Outcome Outcome
}

type key struct {
	query string
	doc   openindex.GlobalDocID
}

// Squash collapses a session's reports so each (query, document) contributes at
// most one report, OR-ing the outcome bits together. A user who clicks the same
// result repeatedly in one session is squashed to a single positive signal, which
// stops one session from inflating a document's click counts (the unsquashed-click
// filter, doc 07.6). It runs on the client before privatization, so the raw
// repetition never leaves the device.
func Squash(session []Report) []Report {
	merged := map[key]Outcome{}
	order := []key{}
	for _, r := range session {
		k := key{r.Query, r.Doc}
		o, seen := merged[k]
		if !seen {
			order = append(order, k)
		}
		merged[k] = Outcome{
			Good:        o.Good || r.Outcome.Good,
			LastLongest: o.LastLongest || r.Outcome.LastLongest,
			Pogo:        o.Pogo || r.Outcome.Pogo,
		}
	}
	out := make([]Report, len(order))
	for i, k := range order {
		out[i] = Report{Query: k.query, Doc: k.doc, Outcome: merged[k]}
	}
	return out
}

// keepProb is the probability a randomized-response bit is reported truthfully,
// e^eps / (1 + e^eps). A larger epsilon keeps more truth (less privacy, less
// noise); epsilon must be positive so the probability exceeds one half and the
// aggregate is recoverable.
func keepProb(epsilon float64) float64 {
	e := math.Exp(epsilon)
	return e / (1 + e)
}

// Privatize applies randomized response to each bit of a report's outcome under
// the privacy budget epsilon: with probability keepProb the bit is reported as
// is, otherwise it is flipped. The noise is added on the client, so what leaves
// the device is already private. Pass a deterministic rng to make a test
// reproducible.
func Privatize(r Report, epsilon float64, rng *rand.Rand) Report {
	p := keepProb(epsilon)
	flip := func(b bool) bool {
		if rng.Float64() < p {
			return b
		}
		return !b
	}
	r.Outcome = Outcome{
		Good:        flip(r.Outcome.Good),
		LastLongest: flip(r.Outcome.LastLongest),
		Pogo:        flip(r.Outcome.Pogo),
	}
	return r
}

// Stats is the de-biased aggregate for one (query, document): the estimated true
// counts of each signal and the number of reports they were estimated from. The
// counts are estimates because each report is noised; they are accurate in
// expectation and tighten as Reports grows.
type Stats struct {
	GoodClicks       float64
	LastLongestClick float64
	Pogo             float64
	Reports          int
}

// counts holds the reported-true tally per signal across the reports for one
// (query, document), plus how many reports there were.
type counts struct {
	good, lastLongest, pogo, n int
}

// Aggregator accumulates privatized reports and recovers the de-biased
// statistics. It is configured with the same epsilon the clients used, because
// the de-biasing math depends on the noise level. It holds only per-(query,
// document) tallies, never a report stream, so there is nothing per-user to
// retain.
type Aggregator struct {
	epsilon float64
	tally   map[key]counts
}

// NewAggregator returns an aggregator for clients using the given privacy budget.
func NewAggregator(epsilon float64) *Aggregator {
	return &Aggregator{epsilon: epsilon, tally: map[key]counts{}}
}

// Observe records one privatized report, tallying its reported-true bits and the
// report count. The de-biasing happens later in Stats.
func (a *Aggregator) Observe(r Report) {
	k := key{r.Query, r.Doc}
	c := a.tally[k]
	if r.Outcome.Good {
		c.good++
	}
	if r.Outcome.LastLongest {
		c.lastLongest++
	}
	if r.Outcome.Pogo {
		c.pogo++
	}
	c.n++
	a.tally[k] = c
}

// Stats returns the de-biased statistics for a (query, document). With n reports
// and c reported-true for a signal, the unbiased estimate of the true count is
// (c - (1-p)*n) / (2p-1), inverting the randomized response. The estimate is
// clamped at zero because noise can push a near-zero signal slightly negative.
func (a *Aggregator) Stats(query string, doc openindex.GlobalDocID) Stats {
	c, ok := a.tally[key{query, doc}]
	if !ok || c.n == 0 {
		return Stats{}
	}
	return Stats{
		GoodClicks:       a.debias(c.good, c.n),
		LastLongestClick: a.debias(c.lastLongest, c.n),
		Pogo:             a.debias(c.pogo, c.n),
		Reports:          c.n,
	}
}

func (a *Aggregator) debias(reportedTrue, n int) float64 {
	p := keepProb(a.epsilon)
	est := (float64(reportedTrue) - (1-p)*float64(n)) / (2*p - 1)
	return max(est, 0)
}
