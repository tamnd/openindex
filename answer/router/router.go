// Package router is the answer engine's economic survival mechanism
// (architecture doc 09.5). Running an LLM on every query is not affordable
// against ten-blue-links, because a frontier model serves far fewer queries per
// second per machine than a web-serving box, so the router decides which path a
// query takes before any model runs:
//
//	RouteSearch   navigational and simple queries go to classic search, no LLM
//	RouteSinglePass   informational queries get one retrieve-rerank-synthesize pass
//	RouteAgentic  complex or multi-hop queries get the deep-research loop (doc 09.4)
//
// In production the classifier is a small fine-tuned model (a 0.5B-class LoRA
// reaches about 90 percent accuracy under 5 ms, doc 09.5), behind the
// Classifier seam. The reference classifier here is a transparent rule set: it
// is correct enough to drive the pipeline and the tests, it documents the
// intent boundaries the trained model has to learn, and it has no dependency.
//
// The failure mode the router guards against is mis-routing a hard query as
// easy, which produces a confidently wrong cheap answer. So the policy biases
// toward escalation: when the signals are mixed it picks the more expensive
// route, and the caller is expected to monitor the escalation rate, because a
// rate above about 30 percent means the cheap tier is too weak (doc 09.5).
package router

import "strings"

// Route is the path a query takes through the engine.
type Route uint8

const (
	// RouteSearch is the classic search path with no model in the loop. It is
	// the right answer for a navigational query, where the user wants a site,
	// not a synthesized paragraph.
	RouteSearch Route = iota
	// RouteSinglePass is one retrieve-rerank-synthesize-cite pass, the common
	// informational answer.
	RouteSinglePass
	// RouteAgentic is the multi-hop deep-research loop, reserved for questions a
	// single retrieval cannot answer because they need a bridge fact.
	RouteAgentic
)

func (r Route) String() string {
	switch r {
	case RouteSearch:
		return "search"
	case RouteSinglePass:
		return "single-pass"
	case RouteAgentic:
		return "agentic"
	default:
		return "unknown"
	}
}

// Decision is a route plus the confidence the classifier had in it. Confidence
// is in [0,1]; the engine can log it and the escalation monitor can watch the
// distribution. A low-confidence simple route is exactly the case the
// escalation bias exists to catch.
type Decision struct {
	Route      Route
	Confidence float32
}

// Classifier maps a query to a route. The production implementation is a small
// trained model over gRPC (doc 09.5); a test uses the reference RuleClassifier
// or a stub. Classify must be cheap, because it runs on every query ahead of
// everything else.
type Classifier interface {
	Classify(query string) Decision
}

// RuleClassifier is the reference Classifier: a transparent rule set over the
// query text. It is deliberately simple and explainable, and it encodes the
// escalation bias so the trained model that replaces it has a behavioral target
// to match.
type RuleClassifier struct {
	// MaxNavigationalWords is the word count at or below which a query with a
	// navigational shape (a bare host, one or two words) is treated as a lookup
	// rather than a question. Zero takes the default.
	MaxNavigationalWords int
}

// DefaultMaxNavigationalWords is the word-count ceiling for treating a short,
// question-free query as navigational.
const DefaultMaxNavigationalWords = 2

// NewRuleClassifier returns a RuleClassifier with the defaults filled in.
func NewRuleClassifier() RuleClassifier {
	return RuleClassifier{MaxNavigationalWords: DefaultMaxNavigationalWords}
}

// multiHopCues are phrases that signal a question whose answer needs a bridge
// fact, which single-pass retrieval tends to miss (doc 09.4).
var multiHopCues = []string{
	"compare", "versus", " vs ", "difference between",
	"how does", "why does", "relationship between",
	"timeline of", "step by step", "and then",
}

// questionCues are phrases that signal an informational intent: the user wants
// an explanation, not a destination.
var questionCues = []string{
	"what", "who", "when", "where", "why", "how",
	"explain", "summarize", "list", "best", "should i",
}

// Classify routes a query by its shape. The order matters: agentic cues win
// over plain question cues (a comparison is informational and multi-hop, and the
// more expensive route is the safe one), and only a short query with no question
// shape at all falls through to classic search.
func (c RuleClassifier) Classify(query string) Decision {
	maxNav := c.MaxNavigationalWords
	if maxNav <= 0 {
		maxNav = DefaultMaxNavigationalWords
	}
	q := strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(q)

	for _, cue := range multiHopCues {
		if strings.Contains(q, cue) {
			return Decision{Route: RouteAgentic, Confidence: 0.8}
		}
	}

	hasQuestion := strings.Contains(q, "?")
	for _, cue := range questionCues {
		if hasFirstWord(words, cue) || strings.Contains(q, " "+cue+" ") {
			hasQuestion = true
			break
		}
	}
	if hasQuestion {
		// A long informational query is more likely to hide a multi-hop need, so
		// escalate it rather than risk a confident single-pass miss.
		if len(words) > 12 {
			return Decision{Route: RouteAgentic, Confidence: 0.55}
		}
		return Decision{Route: RouteSinglePass, Confidence: 0.75}
	}

	if len(words) <= maxNav {
		return Decision{Route: RouteSearch, Confidence: 0.7}
	}
	// A longer query with no question shape is ambiguous. The escalation bias
	// sends it to a single synthesized pass rather than dropping it to bare
	// search, because the cost of a wrong "just search" is a missed answer.
	return Decision{Route: RouteSinglePass, Confidence: 0.5}
}

func hasFirstWord(words []string, w string) bool {
	return len(words) > 0 && words[0] == w
}

// EscalationRate is the share of decisions that did not land on the cheapest
// route (RouteSearch). The router's health check watches it: a rate above about
// 0.3 means the cheap tier is too weak and is escalating too much (doc 09.5).
// It returns 0 for an empty input.
func EscalationRate(decisions []Decision) float64 {
	if len(decisions) == 0 {
		return 0
	}
	var escalated int
	for _, d := range decisions {
		if d.Route != RouteSearch {
			escalated++
		}
	}
	return float64(escalated) / float64(len(decisions))
}

// WantsModel reports whether a route runs the synthesis model at all, so the
// engine can short-circuit a search route without touching the LLM tier.
func WantsModel(r Route) bool {
	return r == RouteSinglePass || r == RouteAgentic
}
