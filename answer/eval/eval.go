// Package eval is the answer engine's own evaluation harness (architecture doc
// 09.6), on top of the relevance harness (doc 07). Correctness and attribution
// are different properties and have to be measured separately, so the metrics
// split into two families: RAGAS-style metrics that diagnose the retriever and
// the generator, and ALCE-style citation metrics that score the grounding.
//
// The metrics here take judgments rather than calling a model, the same shape
// as rank/eval: a caller decides claim-by-claim and citation-by-citation
// entailment (with the same NLI model the online guardrail uses, answer/verify)
// and passes the booleans in, so the metric is a pure, testable function and
// the model is not a hidden dependency of the arithmetic.
//
//	Faithfulness     RAGAS: claims entailed by the context over total claims
//	ContextPrecision RAGAS: are the retrieved passages that matter ranked first
//	ContextRecall    RAGAS: did retrieval find the passages the answer needed
//	CitationRecall   ALCE: do the cited passages entail each sentence (AIS)
//	CitationPrecision ALCE: is each individual citation necessary
package eval

// Faithfulness is the RAGAS grounding metric: the fraction of the answer's
// atomic claims that are entailed by the retrieved context. entailed[i] is the
// caller's verdict for claim i. It is the offline twin of the online guardrail
// in answer/verify, run over a judged query set so a regression in grounding
// fails the build. An answer with no claims is vacuously faithful and scores 1.
func Faithfulness(entailed []bool) float64 {
	if len(entailed) == 0 {
		return 1
	}
	return fraction(entailed)
}

// ContextPrecisionAtK is the RAGAS retriever metric: it rewards ranking the
// passages that matter ahead of the ones that do not. relevant[i] marks whether
// the passage at rank i is relevant, and the score is the mean of precision@i
// taken at each rank that holds a relevant passage, the same construction as
// average precision. A retrieval that puts its relevant passages first scores
// near 1; one that buries them scores low. No relevant passage scores 0.
func ContextPrecisionAtK(relevant []bool, k int) float64 {
	if k > len(relevant) {
		k = len(relevant)
	}
	var hits int
	var sum float64
	for i := range k {
		if relevant[i] {
			hits++
			sum += float64(hits) / float64(i+1)
		}
	}
	if hits == 0 {
		return 0
	}
	return sum / float64(hits)
}

// ContextRecall is the RAGAS metric that needs ground truth: of the claims in
// the reference answer, the fraction whose supporting passage was actually
// retrieved. supported[i] is whether reference claim i can be attributed to the
// retrieved context. It separates a retrieval miss (a needed passage was never
// fetched) from a generation miss. No reference claims scores 1.
func ContextRecall(supported []bool) float64 {
	if len(supported) == 0 {
		return 1
	}
	return fraction(supported)
}

// CitationRecall is the ALCE attribution metric (equivalent to the AIS score):
// the fraction of answer sentences whose concatenated cited passages entail the
// sentence. entailed[i] is the caller's verdict for sentence i, judged with a
// TRUE-class NLI model. It answers "is what the answer says actually supported
// by what it cited". No sentences scores 1.
func CitationRecall(entailed []bool) float64 {
	if len(entailed) == 0 {
		return 1
	}
	return fraction(entailed)
}

// CitationPrecision is the ALCE metric that penalizes padding an answer with
// citations that are not pulling weight: the fraction of individual citations
// that are necessary, where a citation is necessary if removing it breaks the
// entailment of its sentence. necessary[i] is the caller's verdict for citation
// i. A high recall with low precision means the answer cites correctly but
// over-cites. No citations scores 1.
func CitationPrecision(necessary []bool) float64 {
	if len(necessary) == 0 {
		return 1
	}
	return fraction(necessary)
}

// F1 combines a recall and a precision into their harmonic mean, the usual
// summary of the two ALCE citation scores. It returns 0 when both are 0.
func F1(recall, precision float64) float64 {
	if recall+precision == 0 {
		return 0
	}
	return 2 * recall * precision / (recall + precision)
}

// Mean averages a slice of per-query scores into one corpus number, so a harness
// can report a single figure over a query set. An empty input is 0.
func Mean(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	var sum float64
	for _, s := range scores {
		sum += s
	}
	return sum / float64(len(scores))
}

// fraction is the share of true values in a non-empty slice.
func fraction(flags []bool) float64 {
	var t int
	for _, f := range flags {
		if f {
			t++
		}
	}
	return float64(t) / float64(len(flags))
}
