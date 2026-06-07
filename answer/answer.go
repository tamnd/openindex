// Package answer is the AI answer engine (architecture doc 09): the
// retrieve-rerank-synthesize-cite pipeline that turns a query into a grounded,
// cited answer over OpenIndex's own auditable corpus. It runs as cmd/answer,
// behind the mixer (doc 08) and over the Synthesize and Embed gRPC boundaries.
//
// The governing principle is the one from doc 09: only answer what you can
// prove. Every claim in an answer ties to a span of a specific source passage,
// and every citation is checked by entailment before the answer is shown, so a
// model that drifts off the evidence is caught rather than trusted. Because the
// corpus is open, a reader can follow any citation to the archived record and
// check it, which is the point of the whole subsystem.
//
// The pipeline is a four-stage funnel layered on the production retrieval stack
// (docs 05 to 08), because synthesis quality is bounded by retrieval quality:
//
//	Retrieve   hybrid BM25F + dense, fused, pulls 25 to 100 candidate passages
//	Rerank     a cross-encoder filters to the top 3 to 10 (the precision gate)
//	Construct  reranked passages assembled with source tags and ordered for the
//	           lost-in-the-middle effect (synth.Context)
//	Synthesize a strict answer-only-from-context prompt, then verify and cite
//
// Each stage sits behind a seam so the whole pipeline is testable in-process
// without an LLM or a network: Retriever and Reranker stand in for the serving
// tier, synth.Synthesizer for the served model, and verify.Verifier for the NLI
// fact-checker. The router (answer/router) decides which queries reach this
// path at all, because running a model on every query is not survivable (doc
// 09.5).
package answer

import "openindex"

// Passage is a retrieved chunk of a document, the unit the engine works in
// rather than a whole document, because chunk granularity is what the model
// context and the citation spans need (doc 09.1). Score is the retrieval or
// rerank score that ordered it; Published, when set, feeds the freshness
// weighting in synth.
type Passage struct {
	Doc   openindex.GlobalDocID
	URL   string
	Title string
	// Source is the host or publisher the passage came from. Synthesis
	// consolidates per source so a single site cannot outvote the corpus by
	// repeating itself across many passages (doc 09.3).
	Source string
	Text   string
	Score  openindex.Score
	// Published is the document timestamp from the WebTable history (doc 04),
	// zero if unknown. It is a Unix second count to keep the domain type free
	// of a time import; synth converts it when it needs a half-life decay.
	Published int64
}

// Citation links a span of the answer text to the passages that support it. It
// is the in-engine form of the grounding contract; ground.Support is the wire
// form that ships to the client. ChunkIndices index into the passage slice the
// answer was built from.
type Citation struct {
	Start        int // byte offset into Answer.Text, inclusive
	End          int // byte offset into Answer.Text, exclusive
	ChunkIndices []int
}

// Answer is the engine's output: the synthesized text, the passages it was
// grounded in, and the verified citations tying spans of the text to those
// passages. Verified is false when at least one claim failed entailment and was
// dropped or hedged rather than shown as confident, so a caller can surface the
// distinction (doc 09.2).
type Answer struct {
	Text      string
	Passages  []Passage
	Citations []Citation
	Verified  bool
}
