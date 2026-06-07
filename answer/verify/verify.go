// Package verify is the citation contract enforced (architecture doc 09.2): the
// step that decides whether an answer is allowed to be shown. The engine's
// promise is to answer only what it can prove, and this is where that promise
// is checked. The answer is decomposed into atomic claims, each claim is scored
// against its cited passage by an entailment model, and a claim whose citation
// does not entail it is dropped or hedged rather than shown as confident.
//
// The entailment model is the external piece (MiniCheck-class: a 770M model
// reaching GPT-4-level fact-checking at a fraction of the cost, run over gRPC,
// doc 09.2), so it sits behind the Verifier seam. The reference Verifier here
// is a token-overlap heuristic: it is not a real NLI model, but it is a stable
// stand-in that lets the decomposition, the per-claim check, and the
// post-correction policy be built and tested without a model in the loop.
//
// The flow is split into three testable pieces. Decompose turns answer text
// into claims with their byte spans. Check runs the Verifier over each claim
// against its cited passages. Correct applies the policy: keep an entailed
// claim, drop or hedge one that is not.
package verify

import (
	"strings"

	"openindex/answer"
	"openindex/answer/ground"
)

// Claim is one atomic statement from the answer, tied to the byte span it
// occupies and the passage indices it cites. Spans are byte offsets into the
// answer text, the same convention as answer/ground, so a verified claim
// becomes a grounding support without re-finding its offsets.
type Claim struct {
	Text         string
	Start        int // byte offset into the answer, inclusive
	End          int // byte offset, exclusive
	ChunkIndices []int
}

// Verdict is the result of checking one claim: whether its cited passages
// entail it and the model's confidence in [0,1]. A claim with no citation is
// reported as not entailed with zero score, because an uncited claim has no
// evidence to entail it.
type Verdict struct {
	Entailed bool
	Score    float32
}

// Verifier scores whether a passage entails a claim. The production
// implementation is the MiniCheck-class NLI model over gRPC (doc 09.2); a test
// uses the reference OverlapVerifier or a stub. Entail is called once per
// (claim, cited passage) pair, so it must be cheap enough to run on every claim
// of every shown answer as an online guardrail.
type Verifier interface {
	Entail(claim string, passage answer.Passage) Verdict
}

// Decompose splits answer text into atomic claims at sentence boundaries,
// recording each claim's byte span. It is the SRL-style decomposition of doc
// 09.2 reduced to its load-bearing part: one sentence is one claim, which is
// the granularity the entailment check and the citation span both need. The
// citing of each claim is the caller's job (it comes from the model's inline
// markers); Decompose leaves ChunkIndices empty.
//
// Splitting is on sentence-ending punctuation followed by space, operating on
// bytes so the spans line up with answer/ground. Trailing whitespace is trimmed
// from each claim's span so a span does not include the separator.
func Decompose(text string) []Claim {
	var claims []Claim
	b := []byte(text)
	start := 0
	for i := 0; i < len(b); i++ {
		if !isSentenceEnd(b[i]) {
			continue
		}
		// Extend over a run of terminal punctuation ("?!").
		end := i + 1
		for end < len(b) && isSentenceEnd(b[end]) {
			end++
		}
		// A sentence boundary needs whitespace or end-of-text after it, so a
		// decimal point or an abbreviation does not split a claim.
		if end < len(b) && !isSpace(b[end]) {
			i = end - 1
			continue
		}
		claims = appendClaim(claims, b, start, end)
		start = end
	}
	claims = appendClaim(claims, b, start, len(b))
	return claims
}

// appendClaim trims the [start,end) span to its non-space content and appends
// it as a claim, skipping an empty span.
func appendClaim(claims []Claim, b []byte, start, end int) []Claim {
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	if start >= end {
		return claims
	}
	return append(claims, Claim{Text: string(b[start:end]), Start: start, End: end})
}

func isSentenceEnd(c byte) bool { return c == '.' || c == '!' || c == '?' }
func isSpace(c byte) bool       { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// Check runs the Verifier over a claim against every passage it cites and
// returns the best verdict: a claim is entailed if any one of its cited
// passages entails it, since one solid source is enough to support a claim. A
// claim with no citations gets a not-entailed verdict with zero score.
func Check(v Verifier, claim Claim, passages []answer.Passage) Verdict {
	best := Verdict{}
	for _, idx := range claim.ChunkIndices {
		if idx < 0 || idx >= len(passages) {
			continue
		}
		got := v.Entail(claim.Text, passages[idx])
		if got.Entailed && (!best.Entailed || got.Score > best.Score) {
			best = got
		} else if !best.Entailed && got.Score > best.Score {
			best.Score = got.Score
		}
	}
	return best
}

// Result is the outcome of verifying a whole answer: the claims that survived as
// grounding supports, and whether every claim was entailed. Verified is false
// when at least one claim was dropped, so the engine can mark the answer as
// hedged rather than confident.
type Result struct {
	Supports []ground.Support
	Verified bool
}

// MinConfidence is the entailment score below which a claim is treated as
// unsupported even if the model nominally entailed it. It keeps a weak,
// borderline entailment from being shown as a confident citation (doc 09.2).
const MinConfidence = 0.5

// Correct applies the post-correction policy from doc 09.2: it checks every
// claim and keeps the ones whose citation entails them above the confidence
// floor, turning each into a grounding support. A claim that fails is dropped
// from the supports (the engine hedges the answer rather than showing an
// unsupported citation), and its failure flips Verified to false. The threshold
// is configurable; zero takes MinConfidence.
func Correct(v Verifier, claims []Claim, passages []answer.Passage, minConf float32) Result {
	if minConf <= 0 {
		minConf = MinConfidence
	}
	res := Result{Verified: true}
	for _, c := range claims {
		verdict := Check(v, c, passages)
		if !verdict.Entailed || verdict.Score < minConf {
			res.Verified = false
			continue
		}
		res.Supports = append(res.Supports, ground.Support{
			Segment: ground.Segment{Start: c.Start, End: c.End, Text: c.Text},
			Chunks:  c.ChunkIndices,
		})
	}
	return res
}

// OverlapVerifier is the reference Verifier: it scores entailment by the
// fraction of the claim's content words that appear in the passage. It is a
// stand-in for the NLI model, not a replacement, and it documents the contract
// the real model has to meet: a claim grounded in its passage scores high, a
// claim the passage does not mention scores low. Threshold is the overlap
// fraction at or above which the claim counts as entailed; zero takes 0.5.
type OverlapVerifier struct {
	Threshold float32
}

// Entail scores the claim against the passage by content-word overlap.
func (o OverlapVerifier) Entail(claim string, passage answer.Passage) Verdict {
	threshold := o.Threshold
	if threshold <= 0 {
		threshold = 0.5
	}
	claimWords := contentWords(claim)
	if len(claimWords) == 0 {
		return Verdict{}
	}
	passageWords := contentWords(passage.Text)
	have := make(map[string]bool, len(passageWords))
	for _, w := range passageWords {
		have[w] = true
	}
	var hit int
	for _, w := range claimWords {
		if have[w] {
			hit++
		}
	}
	score := float32(hit) / float32(len(claimWords))
	return Verdict{Entailed: score >= threshold, Score: score}
}

// contentWords lowercases the text, splits on non-letter runs, and drops a
// small stopword set, so the overlap score is over meaning-bearing words rather
// than glue words that every passage shares.
func contentWords(s string) []string {
	isWord := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
	}
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !isWord(r)
	})
	out := fields[:0]
	for _, w := range fields {
		if !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "of": true, "to": true, "and": true, "or": true, "in": true,
	"on": true, "at": true, "by": true, "for": true, "with": true, "as": true,
	"that": true, "this": true, "it": true, "its": true, "be": true, "has": true,
}
