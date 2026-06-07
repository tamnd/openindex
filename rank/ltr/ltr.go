// Package ltr is the learning-to-rank model boundary (architecture doc 07.3).
// The production second-stage ranker is LambdaMART, trained and served as a
// separate LightGBM process the Go path calls over a pipe: gradient-boosted
// regression trees trained with LambdaRank gradients, each pairwise gradient
// scaled by the NDCG change from swapping the pair, so the model optimizes the
// ranking metric directly. That follows the engine's policy of Go owning
// orchestration and native code owning the math (impl doc 01).
//
// This package defines the seam the serving path scores through. The production
// LightGBM subprocess and a deep reranker both implement Model; the LinearModel
// here is the in-process reference that exercises the wiring and the cascade
// without an external dependency.
package ltr

import (
	"fmt"

	"openindex/rank/feature"
)

// Model scores one assembled feature vector. A higher score ranks higher. The
// model is stateless across calls and safe for concurrent use.
type Model interface {
	// Score returns the relevance score for one feature vector. The vector
	// length must equal FeatureDim.
	Score(features []float32) float32
	// FeatureDim is the vector length the model was trained for. A caller
	// whose feature.SchemaVersion produced a different length must not use the
	// model; the mismatch is a deploy bug.
	FeatureDim() int
}

// LinearModel is the reference model: a dot product of the feature vector with
// a learned weight vector plus a bias. It is not a tree ensemble, so it cannot
// capture the feature interactions LambdaMART does, but it is enough to drive
// and test the cascade, and a linear model is a legitimate (if weak) ranker.
type LinearModel struct {
	weights []float32
	bias    float32
}

// NewLinearModel returns a model with the given per-feature weights and bias.
// The weight count fixes FeatureDim; align it with feature.NumFeatures for the
// current schema.
func NewLinearModel(weights []float32, bias float32) *LinearModel {
	w := make([]float32, len(weights))
	copy(w, weights)
	return &LinearModel{weights: w, bias: bias}
}

// FeatureDim returns the expected vector length.
func (m *LinearModel) FeatureDim() int { return len(m.weights) }

// Score returns the linear score. A vector of the wrong length scores the bias
// only over the overlap, which a correct caller never triggers; FeatureDim is
// the contract to check against.
func (m *LinearModel) Score(features []float32) float32 {
	n := min(len(m.weights), len(features))
	sum := m.bias
	for i := range n {
		sum += m.weights[i] * features[i]
	}
	return sum
}

// Check verifies that a model's FeatureDim matches the current feature schema.
// The serving path calls it at load time so a model trained on a different
// layout fails fast rather than scoring garbage.
func Check(m Model) error {
	if m.FeatureDim() != int(feature.NumFeatures) {
		return fmt.Errorf("ltr: model expects %d features, schema v%d has %d",
			m.FeatureDim(), feature.SchemaVersion, feature.NumFeatures)
	}
	return nil
}
