package ltr

import (
	"testing"

	"openindex/rank/feature"
)

var _ Model = (*LinearModel)(nil)

func TestLinearScore(t *testing.T) {
	m := NewLinearModel([]float32{1, 2, 3}, 0.5)
	got := m.Score([]float32{1, 1, 1})
	want := float32(1 + 2 + 3 + 0.5)
	if got != want {
		t.Fatalf("Score = %g, want %g", got, want)
	}
}

func TestLinearScoreCopiesWeights(t *testing.T) {
	w := []float32{1, 1}
	m := NewLinearModel(w, 0)
	w[0] = 99 // mutate the caller's slice
	if m.Score([]float32{1, 0}) != 1 {
		t.Fatal("model should hold its own copy of the weights")
	}
}

func TestFeatureDim(t *testing.T) {
	m := NewLinearModel(make([]float32, 7), 0)
	if m.FeatureDim() != 7 {
		t.Fatalf("FeatureDim = %d, want 7", m.FeatureDim())
	}
}

func TestCheckMatchesSchema(t *testing.T) {
	good := NewLinearModel(make([]float32, feature.NumFeatures), 0)
	if err := Check(good); err != nil {
		t.Fatalf("schema-aligned model should pass: %v", err)
	}
	bad := NewLinearModel(make([]float32, feature.NumFeatures+1), 0)
	if Check(bad) == nil {
		t.Fatal("a model with the wrong feature count must fail Check")
	}
}
