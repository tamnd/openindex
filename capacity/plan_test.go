package capacity

import (
	"fmt"
	"testing"
)

func TestBuildSequenceIsOrderedAndGated(t *testing.T) {
	seq := BuildSequence()
	if len(seq) != 10 {
		t.Fatalf("build sequence has %d milestones, want 10 (M0 through M9)", len(seq))
	}
	for i, m := range seq {
		if m.ID != fmt.Sprintf("M%d", i) {
			t.Fatalf("milestone %d has id %q, want M%d (out of order)", i, m.ID, i)
		}
		if m.Gate == "" {
			t.Fatalf("milestone %s has no gate; a milestone is not done until its gate passes", m.ID)
		}
		if len(m.Packages) == 0 {
			t.Fatalf("milestone %s lands no packages", m.ID)
		}
	}
}

// index returns the position of a milestone id in the sequence.
func index(seq []Milestone, id string) int {
	for i, m := range seq {
		if m.ID == id {
			return i
		}
	}
	return -1
}

func TestOpennessLeadsFederation(t *testing.T) {
	seq := BuildSequence()
	// The prime directive: the open artifact (M2) ships before federation (M8),
	// because federation rides on the same signed artifacts and openness is not
	// retrofitted.
	if index(seq, "M2") >= index(seq, "M8") {
		t.Fatal("the open artifact (M2) must precede federation (M8)")
	}
	// Quality before scale: real ranking (M3) and the vector index (M4) precede
	// the scale-out moonshot (M9).
	if index(seq, "M3") >= index(seq, "M9") || index(seq, "M4") >= index(seq, "M9") {
		t.Fatal("ranking and hybrid (M3, M4) must precede scale-out (M9)")
	}
	// The behavioral flywheel (M6) follows the answer engine (M5), which is when
	// there is traffic to learn from.
	if index(seq, "M6") <= index(seq, "M5") {
		t.Fatal("the behavioral flywheel (M6) must follow the answer engine (M5)")
	}
}

func TestRiskRegisterHasOnePrimaryRisk(t *testing.T) {
	risks := RiskRegister()
	if len(risks) == 0 {
		t.Fatal("the risk register must not be empty")
	}
	var primary int
	for _, r := range risks {
		if r.Primary {
			primary++
		}
		if len(r.Mitigations) == 0 {
			t.Fatalf("risk %q is recorded with no mitigations; a risk is recorded to be managed", r.Name)
		}
	}
	if primary != 1 {
		t.Fatalf("the register names %d primary risks, want exactly 1 (index poisoning and Sybil federation)", primary)
	}
	if !risks[0].Primary {
		t.Fatal("the primary risk should lead the register")
	}
}
