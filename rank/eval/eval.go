// Package eval is the relevance harness (architecture doc 07.8). The relevance
// bar is enforced mechanically: these metrics run as a test over a judged query
// set, and a regression past threshold fails the build, so a ranking change
// cannot ship while quietly lowering relevance.
//
// The metrics are the standard ones, computed exactly as the literature defines
// them so results are comparable to BEIR, TREC-DL, and MS MARCO:
//
//	NDCG@k    graded relevance, normalized DCG (the BEIR / TREC-DL standard)
//	MRR@k     reciprocal rank of the first relevant result (the MS MARCO standard)
//	Recall@k  fraction of all relevant documents found in the top k
//	MAP       mean average precision, a recall-oriented complement
//
// Each function takes a ranked list of document ids and a judgment map from
// document id to relevance grade; a document absent from the map is treated as
// grade 0 (not relevant). The functions are generic over the document id type
// so the same harness serves string qrels from a public benchmark and the
// engine's own GlobalDocID.
package eval

import (
	"math"
	"sort"
)

// DCGAtK is the discounted cumulative gain of the top k of a ranked list:
//
//	DCG@k = Sum_{i=1..k} (2^rel_i - 1) / log2(i + 1)
//
// The exponential gain 2^rel - 1 rewards highly relevant documents
// disproportionately, and the logarithmic discount rewards placing them early.
func DCGAtK[D comparable](ranked []D, rel map[D]int, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	var dcg float64
	for i := range k {
		g := rel[ranked[i]]
		if g <= 0 {
			continue
		}
		dcg += (math.Exp2(float64(g)) - 1) / math.Log2(float64(i+2))
	}
	return dcg
}

// NDCGAtK is DCG@k normalized by the ideal DCG@k, the DCG of the best possible
// ordering of the judged documents. It lies in [0,1]; a perfect ranking scores
// 1. With no relevant documents it is 0.
func NDCGAtK[D comparable](ranked []D, rel map[D]int, k int) float64 {
	idcg := idealDCG(rel, k)
	if idcg == 0 {
		return 0
	}
	return DCGAtK(ranked, rel, k) / idcg
}

// idealDCG computes DCG@k over the grades sorted high to low, the maximum DCG
// any ranking of these judgments can reach.
func idealDCG[D comparable](rel map[D]int, k int) float64 {
	grades := make([]int, 0, len(rel))
	for _, g := range rel {
		if g > 0 {
			grades = append(grades, g)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	if k > len(grades) {
		k = len(grades)
	}
	var idcg float64
	for i := range k {
		idcg += (math.Exp2(float64(grades[i])) - 1) / math.Log2(float64(i+2))
	}
	return idcg
}

// MRRAtK is the reciprocal of the 1-based rank of the first relevant document
// in the top k, or 0 if none of the top k is relevant. Relevance is binary
// here: any positive grade counts.
func MRRAtK[D comparable](ranked []D, rel map[D]int, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	for i := range k {
		if rel[ranked[i]] > 0 {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// RecallAtK is the fraction of all relevant documents that appear in the top k.
// It is the first-stage target: the ceiling no later stage can lift, since a
// reranker cannot recover a document the first stage missed. With no relevant
// documents it is 0.
func RecallAtK[D comparable](ranked []D, rel map[D]int, k int) float64 {
	total := numRelevant(rel)
	if total == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	var found int
	for i := range k {
		if rel[ranked[i]] > 0 {
			found++
		}
	}
	return float64(found) / float64(total)
}

// AveragePrecision is the mean of the precision values taken at each rank where
// a relevant document occurs, divided by the total number of relevant
// documents. It rewards both finding relevant documents and ranking them early.
// With no relevant documents it is 0.
func AveragePrecision[D comparable](ranked []D, rel map[D]int) float64 {
	total := numRelevant(rel)
	if total == 0 {
		return 0
	}
	var hits int
	var sum float64
	for i := range ranked {
		if rel[ranked[i]] > 0 {
			hits++
			sum += float64(hits) / float64(i+1)
		}
	}
	return sum / float64(total)
}

func numRelevant[D comparable](rel map[D]int) int {
	var n int
	for _, g := range rel {
		if g > 0 {
			n++
		}
	}
	return n
}

// Mean returns the arithmetic mean of the values, or 0 for an empty slice. It
// is the aggregator that turns a per-query metric (NDCG@k, average precision)
// into the across-query figure a gate checks: mean NDCG@10, or MAP as the mean
// of per-query average precision.
func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
