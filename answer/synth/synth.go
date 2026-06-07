// Package synth builds the context the synthesis model reads and stands in for
// the model itself (architecture doc 09.3). The model is the expensive,
// external piece (an open Apache-2.0 base fine-tuned to emit OpenIndex's
// citation format, served on vLLM/SGLang, doc 09.5), so it sits behind the
// Synthesizer seam and the work that is actually OpenIndex's to get right lives
// here: how the reranked passages are ordered and trimmed before they reach the
// model.
//
// Three context decisions from doc 09.3 are implemented as plain, testable
// functions, because each one is a place naive RAG quietly loses quality:
//
//   - Lost-in-the-middle. A model reads the start and end of its context far
//     better than the middle, so Order places the highest-confidence passages at
//     the edges and the weakest in the middle, rather than in plain rank order.
//   - Utilization. Cramming the window hurts, so Budget fills to a fraction of
//     the token budget (the 40 to 70 percent sweet spot) rather than to the brim.
//   - Conflict and freshness. Consolidate collapses a source that repeats itself
//     so one site cannot outvote the corpus, and Freshen blends a half-life
//     recency decay into the score for time-sensitive intents.
package synth

import (
	"context"
	"math"
	"sort"

	"openindex"
	"openindex/answer"
)

// Synthesizer is the served model: it reads a constructed prompt and streams
// back an answer. The production implementation is the Synthesize gRPC stream
// (doc 02, 09.5); a test uses a deterministic stub. The string returned is the
// raw answer text before grounding and citation, which answer/verify then
// checks and answer/ground annotates.
type Synthesizer interface {
	Synthesize(ctx context.Context, query string, passages []answer.Passage) (string, error)
}

// Order arranges passages for the lost-in-the-middle effect: the strongest
// passages go to the two edges of the context and the weakest sink to the
// middle, where the model attends least. It takes passages in any order, sorts
// a copy by descending score, and deals them outward-in, so rank 1 is first,
// rank 2 is last, rank 3 is second, rank 4 is second-to-last, and so on. The
// input is not mutated.
func Order(passages []answer.Passage) []answer.Passage {
	if len(passages) <= 2 {
		out := make([]answer.Passage, len(passages))
		copy(out, passages)
		return out
	}
	ranked := make([]answer.Passage, len(passages))
	copy(ranked, passages)
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].Score.Less(ranked[j].Score)
	})

	out := make([]answer.Passage, len(ranked))
	lo, hi := 0, len(out)-1
	for i, p := range ranked {
		if i%2 == 0 {
			out[lo] = p
			lo++
		} else {
			out[hi] = p
			hi--
		}
	}
	return out
}

// DefaultUtilization is the fraction of the token budget Budget fills to. It
// sits in the 40 to 70 percent sweet spot from doc 09.3: enough context to
// answer, not so much that the model loses the thread.
const DefaultUtilization = 0.6

// Budget trims passages to fit a token budget at a target utilization. cost
// returns the token cost of a passage (the caller supplies it because tokenizing
// is the model's business, not this package's), util is the fill fraction (zero
// takes the default), and budget is the model's context window in tokens.
// Passages are taken in the order given until the next one would exceed the
// utilization ceiling, so a caller orders first and budgets second.
func Budget(passages []answer.Passage, cost func(answer.Passage) int, util float64, budget int) []answer.Passage {
	if util <= 0 {
		util = DefaultUtilization
	}
	ceiling := int(float64(budget) * util)
	var used int
	var out []answer.Passage
	for _, p := range passages {
		c := cost(p)
		if used+c > ceiling {
			break
		}
		used += c
		out = append(out, p)
	}
	return out
}

// Consolidate collapses passages from the same source down to a per-source cap,
// keeping the highest-scoring passages from each source. It is the Astute-RAG
// move from doc 09.3: a single site that repeats a claim across many passages
// should not get many votes, so no source contributes more than perSource
// passages to the context. Passages with an empty Source are left untouched,
// since they cannot be grouped. The relative order of the survivors is
// preserved so a later Order call still sees a stable input.
func Consolidate(passages []answer.Passage, perSource int) []answer.Passage {
	if perSource <= 0 {
		perSource = 1
	}
	// Rank within each source by score without disturbing the global order: tag
	// each passage with its within-source rank, then keep those under the cap.
	type tagged struct {
		p   answer.Passage
		pos int
	}
	bySource := map[string][]tagged{}
	tags := make([]tagged, len(passages))
	for i, p := range passages {
		t := tagged{p: p, pos: i}
		tags[i] = t
		if p.Source != "" {
			bySource[p.Source] = append(bySource[p.Source], t)
		}
	}
	keep := make(map[int]bool, len(passages))
	for i, p := range passages {
		keep[i] = p.Source == "" // ungrouped passages always survive
	}
	for _, group := range bySource {
		sort.SliceStable(group, func(i, j int) bool {
			return group[i].p.Score.Less(group[j].p.Score)
		})
		for rank, t := range group {
			if rank < perSource {
				keep[t.pos] = true
			}
		}
	}
	out := make([]answer.Passage, 0, len(passages))
	for i, p := range passages {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// Freshen blends a recency boost into each passage score for time-sensitive
// intents (doc 09.3). The fused score is
//
//	score * (1 - weight) + recency * weight * scoreScale
//
// where recency is a half-life decay of the passage age in seconds: a passage
// published now scores 1, one half-life old scores 0.5, and so on. now and
// halfLife are passed in (the domain type stays free of a time import), weight
// in [0,1] sets how much recency matters, and scoreScale puts the recency term
// on the same order as the retrieval score. A passage with no timestamp
// (Published == 0) is left unchanged, because guessing its age would be worse
// than ignoring recency for it. The input is not mutated.
func Freshen(passages []answer.Passage, now, halfLife int64, weight, scoreScale float64) []answer.Passage {
	out := make([]answer.Passage, len(passages))
	copy(out, passages)
	if weight <= 0 || halfLife <= 0 {
		return out
	}
	for i := range out {
		p := &out[i]
		if p.Published == 0 {
			continue
		}
		age := max(now-p.Published, 0)
		recency := halfLifeDecay(age, halfLife)
		fused := float64(p.Score)*(1-weight) + recency*weight*scoreScale
		p.Score = openindexScore(fused)
	}
	return out
}

// halfLifeDecay is 0.5 ^ (age / halfLife): 1 at age 0, 0.5 at one half-life.
func halfLifeDecay(age, halfLife int64) float64 {
	return math.Exp2(-float64(age) / float64(halfLife))
}

// openindexScore clamps a fused float64 back into a non-negative Score, since a
// recency blend should never drive a score below zero.
func openindexScore(v float64) openindex.Score {
	if v < 0 {
		v = 0
	}
	return openindex.Score(v)
}
