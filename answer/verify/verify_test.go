package verify

import (
	"testing"

	"openindex/answer"
)

func passage(text string) answer.Passage { return answer.Passage{Text: text} }

func TestDecomposeSplitsSentences(t *testing.T) {
	claims := Decompose("Cats purr. Dogs bark! Do fish sleep?")
	if len(claims) != 3 {
		t.Fatalf("expected 3 claims, got %d: %+v", len(claims), claims)
	}
	want := []string{"Cats purr.", "Dogs bark!", "Do fish sleep?"}
	for i, w := range want {
		if claims[i].Text != w {
			t.Fatalf("claim %d: got %q want %q", i, claims[i].Text, w)
		}
	}
}

func TestDecomposeSpansAreByteAccurate(t *testing.T) {
	text := "Cats purr. Dogs bark."
	claims := Decompose(text)
	for _, c := range claims {
		if got := text[c.Start:c.End]; got != c.Text {
			t.Fatalf("span [%d,%d) is %q, want %q", c.Start, c.End, got, c.Text)
		}
	}
}

func TestDecomposeDoesNotSplitDecimal(t *testing.T) {
	// A period not followed by space is not a boundary, so "3.14" stays whole.
	claims := Decompose("Pi is about 3.14 in value.")
	if len(claims) != 1 {
		t.Fatalf("a decimal point should not split the claim, got %d: %+v", len(claims), claims)
	}
}

func TestDecomposeTrailingFragment(t *testing.T) {
	// A final sentence with no terminal punctuation is still a claim.
	claims := Decompose("First. A trailing thought")
	if len(claims) != 2 || claims[1].Text != "A trailing thought" {
		t.Fatalf("trailing fragment not captured: %+v", claims)
	}
}

func TestOverlapVerifierEntailsGrounded(t *testing.T) {
	v := OverlapVerifier{}
	got := v.Entail("Cats purr when content", passage("A cat will purr when it is content and relaxed"))
	if !got.Entailed {
		t.Fatalf("a claim grounded in the passage should entail, score %g", got.Score)
	}
}

func TestOverlapVerifierRejectsUngrounded(t *testing.T) {
	v := OverlapVerifier{}
	got := v.Entail("Quasars emit radio bursts", passage("A cat will purr when it is content"))
	if got.Entailed {
		t.Fatalf("a claim absent from the passage should not entail, score %g", got.Score)
	}
}

func TestCheckTakesBestCitedPassage(t *testing.T) {
	v := OverlapVerifier{}
	passages := []answer.Passage{
		passage("unrelated text about weather"),
		passage("a cat will purr when content and relaxed"),
	}
	claim := Claim{Text: "Cats purr when content", ChunkIndices: []int{0, 1}}
	got := Check(v, claim, passages)
	if !got.Entailed {
		t.Fatalf("one entailing passage among the citations should pass, score %g", got.Score)
	}
}

func TestCheckUncitedClaimFails(t *testing.T) {
	v := OverlapVerifier{}
	got := Check(v, Claim{Text: "anything"}, []answer.Passage{passage("anything at all")})
	if got.Entailed {
		t.Fatal("a claim with no citation cannot be entailed")
	}
}

func TestCorrectKeepsEntailedDropsRest(t *testing.T) {
	v := OverlapVerifier{}
	passages := []answer.Passage{
		passage("a cat will purr when content and relaxed"),
		passage("weather forecast for the weekend"),
	}
	claims := []Claim{
		{Text: "Cats purr when content", Start: 0, End: 22, ChunkIndices: []int{0}},
		{Text: "Mars has two moons", Start: 23, End: 41, ChunkIndices: []int{1}},
	}
	res := Correct(v, claims, passages, 0)
	if res.Verified {
		t.Fatal("an answer with an unsupported claim should not be marked verified")
	}
	if len(res.Supports) != 1 {
		t.Fatalf("only the entailed claim should become a support, got %d", len(res.Supports))
	}
	if res.Supports[0].Segment.Text != "Cats purr when content" {
		t.Fatalf("wrong claim survived: %q", res.Supports[0].Segment.Text)
	}
}

func TestCorrectAllEntailedIsVerified(t *testing.T) {
	v := OverlapVerifier{}
	passages := []answer.Passage{passage("a cat will purr when content and relaxed")}
	claims := []Claim{{Text: "Cats purr when content", Start: 0, End: 22, ChunkIndices: []int{0}}}
	res := Correct(v, claims, passages, 0)
	if !res.Verified || len(res.Supports) != 1 {
		t.Fatalf("a fully grounded answer should verify with one support, got verified=%v supports=%d", res.Verified, len(res.Supports))
	}
}

func TestCorrectHonorsConfidenceFloor(t *testing.T) {
	// A weak but nonzero overlap should be dropped by a high floor even though
	// the verifier nominally entailed it at its own low threshold.
	v := OverlapVerifier{Threshold: 0.1}
	passages := []answer.Passage{passage("content relaxed and a few other unrelated words here too")}
	claims := []Claim{{Text: "Cats purr loudly when content", Start: 0, End: 29, ChunkIndices: []int{0}}}
	res := Correct(v, claims, passages, 0.9)
	if res.Verified || len(res.Supports) != 0 {
		t.Fatalf("a weak entailment should not clear a 0.9 floor, got verified=%v supports=%d", res.Verified, len(res.Supports))
	}
}
