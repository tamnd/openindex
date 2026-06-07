package frontier

import (
	"container/heap"
	"sync"
	"time"
)

// URL is a frontier entry: the address to fetch plus the scheduling inputs the
// queue needs. Host is the politeness key (one host is fetched by at most one
// worker at a time); Priority biases which front queue it lands in; Fingerprint
// is the dedup key checked against the SeenSet.
type URL struct {
	Raw         string
	Host        string
	Priority    int // higher is crawled sooner; clamped into [0, Priorities)
	Fingerprint uint64
}

// Priorities is the number of front queues. A URL's Priority selects its queue;
// higher-priority queues are drained preferentially, which is how a freshness or
// importance signal (PageRank, change rate) steers crawl order (04.2).
const Priorities = 8

// politeness derives the minimum gap before a host may be fetched again from how
// long its last fetch took: gap = factor * lastFetchDuration, bounded by min/max.
// This is Mercator's adaptive back-off - slow hosts are hit less often - rather
// than a flat delay (04.2). A robots crawl-delay, when present, raises the floor.
type politeness struct {
	factor   int
	min, max time.Duration
}

func defaultPoliteness() politeness {
	return politeness{factor: 10, min: 500 * time.Millisecond, max: 30 * time.Second}
}

func (p politeness) gap(lastFetch, crawlDelay time.Duration) time.Duration {
	g := time.Duration(p.factor) * lastFetch
	g = max(g, p.min)
	g = min(g, p.max)
	g = max(g, crawlDelay)
	return g
}

// hostState tracks when a host next becomes eligible and whether a worker
// currently holds it.
type hostState struct {
	host       string
	readyAt    time.Time
	crawlDelay time.Duration
	priority   int // highest priority seen for this host, for heap tiebreak
	busy       bool
	queue      []URL // back queue: URLs waiting for this host
	index      int   // heap index, maintained by the heap
}

// Frontier is a Mercator-style scheduler: front queues hold prioritized URLs
// not yet assigned to a host; back queues (one per host) enforce single-host
// politeness; a min-heap on readyAt orders hosts by when they may next be
// fetched. It is safe for concurrent use by many crawl workers.
type Frontier struct {
	mu sync.Mutex

	seen       SeenSet
	politeness politeness
	now        func() time.Time

	front [Priorities][]URL // prioritized URLs awaiting a back queue
	hosts map[string]*hostState
	ready hostHeap // hosts with a non-empty back queue, ordered by readyAt

	pending int // total URLs held (front + back), for Len and idle detection
}

// Option configures a Frontier.
type Option func(*Frontier)

// WithClock overrides the time source, for deterministic tests.
func WithClock(now func() time.Time) Option { return func(f *Frontier) { f.now = now } }

// WithPolitenessFactor sets the multiplier applied to a host's last fetch
// duration to derive its next gap.
func WithPolitenessFactor(factor int) Option {
	return func(f *Frontier) { f.politeness.factor = factor }
}

// New returns an empty Frontier using the given SeenSet for dedup.
func New(seen SeenSet, opts ...Option) *Frontier {
	f := &Frontier{
		seen:       seen,
		politeness: defaultPoliteness(),
		now:        time.Now,
		hosts:      make(map[string]*hostState),
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// Add admits u to the frontier unless its fingerprint has been seen before.
// It returns true if the URL was newly admitted. A URL with no Host is dropped
// (the fetcher cannot schedule it politely).
func (f *Frontier) Add(u URL) bool {
	if u.Host == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen.Seen(u.Fingerprint) {
		return false
	}
	p := min(max(u.Priority, 0), Priorities-1)
	f.front[p] = append(f.front[p], u)
	f.pending++
	f.assignLocked()
	return true
}

// SetCrawlDelay records a robots crawl-delay hint for a host; it raises the
// politeness floor for that host's subsequent fetches (04.3).
func (f *Frontier) SetCrawlDelay(host string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hs := f.hostLocked(host)
	hs.crawlDelay = d
}

// Next returns the next URL ready to fetch and true, or false if nothing is
// eligible right now (either the frontier is empty or every ready host is still
// in its politeness gap). The caller fetches the URL and must call Done with how
// long the fetch took so the host's next gap can be computed. The returned URL's
// host is marked busy and will not be handed out again until Done.
func (f *Frontier) Next() (URL, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assignLocked()
	if f.ready.Len() == 0 {
		return URL{}, false
	}
	top := f.ready[0]
	if top.busy || top.readyAt.After(f.now()) {
		return URL{}, false
	}
	u := top.queue[0]
	top.queue = top.queue[1:]
	top.busy = true
	f.pending--
	// A busy host leaves the ready heap until Done re-admits it; this guarantees
	// single-host concurrency.
	heap.Pop(&f.ready)
	return u, true
}

// Done reports that the host of u has finished a fetch that took fetchDuration.
// It schedules the host's next eligibility at now + gap(fetchDuration) and, if
// the host still has queued URLs, returns it to the ready heap.
func (f *Frontier) Done(u URL, fetchDuration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hs, ok := f.hosts[u.Host]
	if !ok {
		return
	}
	hs.busy = false
	gap := f.politeness.gap(fetchDuration, hs.crawlDelay)
	hs.readyAt = f.now().Add(gap)
	if len(hs.queue) > 0 {
		heap.Push(&f.ready, hs)
	}
}

// Len reports how many URLs are held across the front and back queues, not
// counting a URL currently checked out by a worker.
func (f *Frontier) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending
}

// assignLocked drains front-queue URLs into their per-host back queues,
// highest priority first, pushing newly non-empty hosts onto the ready heap.
// Caller holds f.mu.
func (f *Frontier) assignLocked() {
	for p := Priorities - 1; p >= 0; p-- {
		for _, u := range f.front[p] {
			hs := f.hostLocked(u.Host)
			wasEmpty := len(hs.queue) == 0
			hs.queue = append(hs.queue, u)
			hs.priority = max(hs.priority, u.Priority)
			if wasEmpty && !hs.busy {
				heap.Push(&f.ready, hs)
			}
		}
		f.front[p] = f.front[p][:0]
	}
}

// hostLocked returns the host state, creating it ready-now on first sight.
func (f *Frontier) hostLocked(host string) *hostState {
	hs, ok := f.hosts[host]
	if !ok {
		hs = &hostState{host: host, readyAt: f.now(), index: -1}
		f.hosts[host] = hs
	}
	return hs
}

// hostHeap is a min-heap of host states ordered by readyAt, so the host that
// may next be fetched is always at the root.
type hostHeap []*hostState

func (h hostHeap) Len() int { return len(h) }
func (h hostHeap) Less(i, j int) bool {
	if !h[i].readyAt.Equal(h[j].readyAt) {
		return h[i].readyAt.Before(h[j].readyAt)
	}
	// Same readiness: the higher-priority host is fetched first.
	return h[i].priority > h[j].priority
}
func (h hostHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *hostHeap) Push(x any) {
	hs := x.(*hostState)
	hs.index = len(*h)
	*h = append(*h, hs)
}

func (h *hostHeap) Pop() any {
	old := *h
	n := len(old)
	hs := old[n-1]
	old[n-1] = nil
	hs.index = -1
	*h = old[:n-1]
	return hs
}
