package control

import "testing"

func TestHealthyByDefault(t *testing.T) {
	h := NewHealthTracker()
	if !h.InFanout("n1") {
		t.Fatal("an unseen node should be healthy and in fan-out")
	}
}

func TestEntersProbationWhenSlow(t *testing.T) {
	h := NewHealthTracker()
	// Feed latencies well above the enter threshold until the smoothed value
	// crosses it.
	var st Health
	for range 50 {
		st = h.Observe("n1", 500)
	}
	if st != Probation {
		t.Fatalf("a chronically slow node should enter probation, state=%v", st)
	}
	if h.InFanout("n1") {
		t.Fatal("a node on probation should be out of fan-out")
	}
}

func TestRecoversFromProbation(t *testing.T) {
	h := NewHealthTracker()
	for range 50 {
		h.Observe("n1", 500)
	}
	if h.InFanout("n1") {
		t.Fatal("precondition: node should be on probation")
	}
	// Now it speeds back up below the leave threshold.
	for range 50 {
		h.Observe("n1", 20)
	}
	if !h.InFanout("n1") {
		t.Fatal("a recovered node should return to fan-out")
	}
}

func TestHysteresisAvoidsFlapping(t *testing.T) {
	h := NewHealthTracker()
	// Drive it onto probation.
	for range 50 {
		h.Observe("n1", 500)
	}
	if h.State("n1") != Probation {
		t.Fatal("precondition: node should be on probation")
	}
	// Latency between the leave floor (120) and the enter ceiling (200) must not
	// flip it back to healthy: it stays on probation until it clears the floor.
	for range 50 {
		if got := h.Observe("n1", 150); got != Probation {
			t.Fatalf("a node in the hysteresis band should stay on probation, got %v", got)
		}
	}
}
