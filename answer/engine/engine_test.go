package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"openindex/answer"
	"openindex/answer/router"
	"openindex/answer/verify"
)

// stub seams for the pipeline.

type stubRetriever struct {
	passages []answer.Passage
	err      error
}

func (s stubRetriever) Retrieve(_ context.Context, _ string, _ int) ([]answer.Passage, error) {
	return s.passages, s.err
}

// passthroughReranker keeps the first k passages, standing in for the
// cross-encoder without scoring.
type passthroughReranker struct{ err error }

func (r passthroughReranker) Rerank(_ context.Context, _ string, ps []answer.Passage, k int) ([]answer.Passage, error) {
	if r.err != nil {
		return nil, r.err
	}
	if k < len(ps) {
		ps = ps[:k]
	}
	return ps, nil
}

// fixedSynth returns a canned answer with inline citation markers.
type fixedSynth struct {
	text string
	err  error
}

func (f fixedSynth) Synthesize(_ context.Context, _ string, _ []answer.Passage) (string, error) {
	return f.text, f.err
}

// fixedRoute classifies every query to one route.
type fixedRoute struct{ route router.Route }

func (f fixedRoute) Classify(string) router.Decision { return router.Decision{Route: f.route} }

func psg(source, text string) answer.Passage {
	return answer.Passage{Source: source, Text: text, Score: 1}
}

func newEngine(r Retriever, syn fixedSynth) Engine {
	return Engine{
		Classifier:  fixedRoute{router.RouteSinglePass},
		Retriever:   r,
		Reranker:    passthroughReranker{},
		Synthesizer: syn,
		Verifier:    verify.OverlapVerifier{},
		Config:      Defaults(),
	}
}

func TestEngineGroundsAndCites(t *testing.T) {
	passages := []answer.Passage{
		psg("a.test", "a cat will purr when it is content and relaxed"),
		psg("b.test", "the moon orbits the earth roughly every twenty seven days"),
	}
	syn := fixedSynth{text: "Cats purr when content. [1] The moon orbits the earth. [2]"}
	e := newEngine(stubRetriever{passages: passages}, syn)

	ans, err := e.Answer(context.Background(), "tell me facts")
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Verified {
		t.Fatalf("both claims are grounded, answer should be verified: %q", ans.Text)
	}
	if len(ans.Citations) != 2 {
		t.Fatalf("expected two citations, got %d", len(ans.Citations))
	}
	// The visible markers come back in the text.
	if !strings.Contains(ans.Text, "[1]") || !strings.Contains(ans.Text, "[2]") {
		t.Fatalf("citation markers missing from answer: %q", ans.Text)
	}
}

func TestEngineDropsUnsupportedClaim(t *testing.T) {
	passages := []answer.Passage{
		psg("a.test", "a cat will purr when it is content and relaxed"),
		psg("b.test", "an unrelated passage about tax law and filing deadlines"),
	}
	// The second claim cites passage 2, which does not support it.
	syn := fixedSynth{text: "Cats purr when content. [1] Mars has fourteen moons. [2]"}
	e := newEngine(stubRetriever{passages: passages}, syn)

	ans, err := e.Answer(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if ans.Verified {
		t.Fatal("an unsupported claim should leave the answer unverified")
	}
	if len(ans.Citations) != 1 {
		t.Fatalf("only the supported claim should be cited, got %d", len(ans.Citations))
	}
}

func TestEngineCitationSpansAreAccurate(t *testing.T) {
	passages := []answer.Passage{psg("a.test", "a cat will purr when it is content and relaxed")}
	syn := fixedSynth{text: "Cats purr when content. [1]"}
	e := newEngine(stubRetriever{passages: passages}, syn)

	ans, err := e.Answer(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Citations) != 1 {
		t.Fatalf("expected one citation, got %d", len(ans.Citations))
	}
	c := ans.Citations[0]
	// The cited span must fall on the clean text and read as the sentence.
	if got := ans.Text[c.Start:c.End]; got != "Cats purr when content." {
		t.Fatalf("citation span is %q, want the sentence", got)
	}
}

func TestEngineSearchRouteShortCircuits(t *testing.T) {
	e := newEngine(stubRetriever{}, fixedSynth{text: "should not run"})
	e.Classifier = fixedRoute{router.RouteSearch}
	_, err := e.Answer(context.Background(), "github login")
	if !errors.Is(err, ErrSearchRoute) {
		t.Fatalf("a search-routed query should return ErrSearchRoute, got %v", err)
	}
}

func TestEngineRetrieverErrorPropagates(t *testing.T) {
	boom := errors.New("retrieve failed")
	e := newEngine(stubRetriever{err: boom}, fixedSynth{})
	if _, err := e.Answer(context.Background(), "q"); !errors.Is(err, boom) {
		t.Fatalf("a retrieval error should propagate, got %v", err)
	}
}

func TestEngineSynthErrorPropagates(t *testing.T) {
	boom := errors.New("model down")
	passages := []answer.Passage{psg("a", "text")}
	e := newEngine(stubRetriever{passages: passages}, fixedSynth{err: boom})
	if _, err := e.Answer(context.Background(), "q"); !errors.Is(err, boom) {
		t.Fatalf("a synthesis error should propagate, got %v", err)
	}
}

func TestParseCitationsStripsMarkers(t *testing.T) {
	clean, claims := parseCitations("Cats purr. [1][2] Dogs bark. [3]")
	if clean != "Cats purr. Dogs bark." {
		t.Fatalf("clean text wrong: %q", clean)
	}
	if len(claims) != 2 {
		t.Fatalf("expected two claims, got %d: %+v", len(claims), claims)
	}
	if len(claims[0].ChunkIndices) != 2 || claims[0].ChunkIndices[0] != 0 || claims[0].ChunkIndices[1] != 1 {
		t.Fatalf("first claim citations wrong: %+v", claims[0].ChunkIndices)
	}
	if len(claims[1].ChunkIndices) != 1 || claims[1].ChunkIndices[0] != 2 {
		t.Fatalf("second claim citations wrong: %+v", claims[1].ChunkIndices)
	}
	// Spans must address the clean text.
	for _, c := range claims {
		if clean[c.Start:c.End] != c.Text {
			t.Fatalf("span [%d,%d)=%q does not match %q", c.Start, c.End, clean[c.Start:c.End], c.Text)
		}
	}
}

func TestParseCitationsNoMarkers(t *testing.T) {
	clean, claims := parseCitations("Just a sentence with no citation.")
	if clean != "Just a sentence with no citation." {
		t.Fatalf("clean text changed: %q", clean)
	}
	if len(claims) != 1 || len(claims[0].ChunkIndices) != 0 {
		t.Fatalf("expected one uncited claim, got %+v", claims)
	}
}

func TestParseCitationsTrailingFragment(t *testing.T) {
	clean, claims := parseCitations("First fact. [1] a trailing thought")
	if clean != "First fact. a trailing thought" {
		t.Fatalf("clean text wrong: %q", clean)
	}
	if len(claims) != 2 {
		t.Fatalf("expected two claims, got %d: %+v", len(claims), claims)
	}
	if claims[1].Text != "a trailing thought" || len(claims[1].ChunkIndices) != 0 {
		t.Fatalf("trailing fragment wrong: %+v", claims[1])
	}
}
