package router

import "testing"

func TestNavigationalGoesToSearch(t *testing.T) {
	c := NewRuleClassifier()
	for _, q := range []string{"github", "openindex login", "espn"} {
		if d := c.Classify(q); d.Route != RouteSearch {
			t.Fatalf("%q: got %v, want search", q, d.Route)
		}
	}
}

func TestInformationalGoesToSinglePass(t *testing.T) {
	c := NewRuleClassifier()
	for _, q := range []string{
		"what is a bloom filter",
		"who wrote the go memory model",
		"explain consistent hashing",
		"is sqlite good for production?",
	} {
		if d := c.Classify(q); d.Route != RouteSinglePass {
			t.Fatalf("%q: got %v, want single-pass", q, d.Route)
		}
	}
}

func TestMultiHopGoesToAgentic(t *testing.T) {
	c := NewRuleClassifier()
	for _, q := range []string{
		"compare bm25 and dense retrieval",
		"raft versus paxos",
		"difference between tcp and quic",
		"how does pagerank relate to trustrank",
	} {
		if d := c.Classify(q); d.Route != RouteAgentic {
			t.Fatalf("%q: got %v, want agentic", q, d.Route)
		}
	}
}

func TestMultiHopCueBeatsPlainQuestion(t *testing.T) {
	// "how does X compare to Y" has both a question cue and a multi-hop cue; the
	// expensive route must win, because the cost of a confident single-pass miss
	// on a comparison is the failure the router exists to prevent.
	c := NewRuleClassifier()
	if d := c.Classify("how does a skip list compare to a btree"); d.Route != RouteAgentic {
		t.Fatalf("got %v, want agentic", d.Route)
	}
}

func TestLongInformationalEscalates(t *testing.T) {
	c := NewRuleClassifier()
	q := "what are the main tradeoffs an engineer should weigh when choosing a storage engine for a write heavy workload"
	if d := c.Classify(q); d.Route != RouteAgentic {
		t.Fatalf("a long informational query should escalate, got %v", d.Route)
	}
}

func TestAmbiguousLongQueryDoesNotDropToSearch(t *testing.T) {
	// No question shape, but too long to be navigational. The escalation bias
	// sends it to a synthesized pass rather than bare search.
	c := NewRuleClassifier()
	if d := c.Classify("go memory model happens before ordering atomics"); d.Route != RouteSinglePass {
		t.Fatalf("got %v, want single-pass", d.Route)
	}
}

func TestEscalationRate(t *testing.T) {
	ds := []Decision{
		{Route: RouteSearch}, {Route: RouteSearch},
		{Route: RouteSinglePass}, {Route: RouteAgentic},
	}
	if got := EscalationRate(ds); got != 0.5 {
		t.Fatalf("got %g, want 0.5", got)
	}
	if got := EscalationRate(nil); got != 0 {
		t.Fatalf("empty should be 0, got %g", got)
	}
}

func TestWantsModel(t *testing.T) {
	if WantsModel(RouteSearch) {
		t.Fatal("the search route should not touch the model")
	}
	if !WantsModel(RouteSinglePass) || !WantsModel(RouteAgentic) {
		t.Fatal("both synthesis routes should want the model")
	}
}

func TestRouteString(t *testing.T) {
	cases := map[Route]string{
		RouteSearch:     "search",
		RouteSinglePass: "single-pass",
		RouteAgentic:    "agentic",
		Route(99):       "unknown",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Fatalf("Route(%d): got %q want %q", r, got, want)
		}
	}
}
