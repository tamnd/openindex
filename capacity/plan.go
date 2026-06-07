// The build sequence and the risk register of implementation doc 13.5 and 13.6,
// as data a test can check rather than prose a reader has to trust. The ordering
// principle is the prime directive (doc 01): openness leads, every milestone
// clears the relevance and latency bar before it ships, and decentralization is
// added only where it does not cost relevance or latency.

package capacity

// Milestone is one step of the build sequence: its id, what it delivers, the
// packages it lands, and the implementation gate it must clear before the next
// milestone starts. The gate is the point of the plan: a milestone is not done
// when the code compiles, it is done when its gate passes, and the gates are the
// relevance and latency harness of doc 12, not reviewer judgment.
type Milestone struct {
	ID       string
	Title    string
	Packages []string
	Gate     string
}

// BuildSequence returns the milestone plan M0 through M9 in build order (doc
// 13.5). The order encodes the sequencing levers: bootstrap on Common Crawl so
// the crawler does not gate the first index, quality before scale, openness from
// the first artifact (M2) rather than retrofitted, and the build-versus-bind
// decision deferred to where measured evidence exists (M4).
func BuildSequence() []Milestone {
	return []Milestone{
		{
			ID:       "M0",
			Title:    "Single-node searchable index",
			Packages: []string{"index", "crawler", "rank", "rank/eval"},
			Gate:     "NDCG@10 within reach of an Elasticsearch baseline on a Common Crawl slice, single node",
		},
		{
			ID:       "M1",
			Title:    "Distributed serving",
			Packages: []string{"serve", "control"},
			Gate:     "roughly 200ms P99 across a multi-shard fleet with the good-enough cutoff and hedged requests",
		},
		{
			ID:       "M2",
			Title:    "The open artifact",
			Packages: []string{"open", "open/ciff"},
			Gate:     "an independent CIFF-aware engine loads the export and reproduces results",
		},
		{
			ID:       "M3",
			Title:    "Real ranking",
			Packages: []string{"rank"},
			Gate:     "NDCG@10 materially above the M0 baseline with spam-set precision measured",
		},
		{
			ID:       "M4",
			Title:    "Hybrid and the vector index",
			Packages: []string{"vector", "rank"},
			Gate:     "hybrid lifting Recall@k and NDCG@10 over M3 within the latency budget",
		},
		{
			ID:       "M5",
			Title:    "The answer engine",
			Packages: []string{"answer", "answer/eval"},
			Gate:     "RAGAS faithfulness and ALCE citation recall above the retrieve-then-read baselines, TTFT 1.3 to 1.9s, router holding cost in budget",
		},
		{
			ID:       "M6",
			Title:    "The behavioral flywheel",
			Packages: []string{"open/crowdsignal", "rank"},
			Gate:     "online interleaving improvement with no privacy regression",
		},
		{
			ID:       "M7",
			Title:    "Crawl at scale",
			Packages: []string{"crawler"},
			Gate:     "sustained polite, consent-compliant crawl throughput with near-real-time freshness",
		},
		{
			ID:       "M8",
			Title:    "Federation",
			Packages: []string{"open", "open/federation"},
			Gate:     "an independent operator serving a partition within the latency budget whose rebuild matches the signed snapshot, and a tainted shard rejected",
		},
		{
			ID:       "M9",
			Title:    "Scale-out and verticals",
			Packages: []string{"serve", "capacity"},
			Gate:     "the capacity and cost models holding at scale with P99 intact",
		},
	}
}

// Risk is one entry of the risk register: a named risk, whether it is the
// primary one, and how it is managed. The differentiators create the risks, so
// they are recorded to be managed rather than discovered (doc 13.6).
type Risk struct {
	Name        string
	Primary     bool
	Mitigations []string
}

// RiskRegister returns the recorded risks (doc 13.6). The primary risk is index
// poisoning and Sybil federation, because the open, downloadable,
// federation-hostable index is the differentiator that is also the open attack
// surface, and it is never fully solved, only managed.
func RiskRegister() []Risk {
	return []Risk{
		{
			Name:    "Index poisoning and Sybil federation",
			Primary: true,
			Mitigations: []string{
				"day-one spam stack",
				"signed content-addressed shards",
				"reproducible-build verification",
				"operator reputation gating",
			},
		},
		{
			Name: "The economics of an LLM per query",
			Mitigations: []string{
				"router to the no-LLM and small-model paths",
				"continuous batching",
				"FP8 hardware",
				"conservative semantic caching",
				"frontier models as a routed API tier, not a hosted fleet",
			},
		},
		{
			Name: "Cold-start relevance",
			Mitigations: []string{
				"Common Crawl bootstrap",
				"the crowd signal at M6",
				"antitrust data-sharing as an accelerant, not a dependency",
			},
		},
		{
			Name: "Legal and governance of the corpus",
			Mitigations: []string{
				"consent at crawl time",
				"the Common Crawl precedent",
				"a foundation or consortium governance model",
			},
		},
		{
			Name: "Crawl politeness and reputation",
			Mitigations: []string{
				"verifiable good-citizen identity (FCrDNS, published IP ranges, honored REP)",
				"conservative per-host budgeting",
				"the consent layer",
			},
		},
		{
			Name: "Build-versus-bind drift",
			Mitigations: []string{
				"keep the native boundary small, profiled, and contained in vector and the inference processes",
				"defer the bind decision to M4",
			},
		},
	}
}
