// Latency-induced probation (architecture doc 10.4). This is distinct from
// liveness: liveness is the lease (a node either holds it or it does not), while
// health is a serving-tier judgment about a node that is alive but chronically
// slow. The root excludes such a node from fan-out so one sick replica does not
// blow the tail latency, while still sending it shadow requests so its recovery
// is observed, and reincorporates it when it speeds back up.
//
// The tracker smooths latency with an exponentially weighted moving average and
// switches state with hysteresis: a node enters probation above a high threshold
// and leaves only below a lower one, so a node hovering near the line does not
// flap in and out of the fan-out set on every sample.

package control

import "sync"

// Health is a node's serving-tier health state. It is separate from membership:
// a node can be a member (lease held) and still be on probation.
type Health uint8

const (
	// Healthy means the node is included in fan-out.
	Healthy Health = iota
	// Probation means the node is excluded from fan-out but still shadowed.
	Probation
)

// HealthTracker holds the smoothed latency and probation state per node. It is
// safe for concurrent use: the serving path records samples while the root reads
// the fan-out decision.
type HealthTracker struct {
	mu    sync.Mutex
	ewma  map[NodeID]float64
	state map[NodeID]Health

	// Alpha is the EWMA weight on the newest sample, in (0, 1]. A larger value
	// reacts faster and is noisier.
	Alpha float64
	// EnterMillis is the smoothed-latency ceiling above which a healthy node
	// enters probation.
	EnterMillis float64
	// LeaveMillis is the floor below which a node on probation returns to
	// healthy. It is lower than EnterMillis, and the gap is the hysteresis that
	// stops flapping.
	LeaveMillis float64
}

// NewHealthTracker returns a tracker with defaults sized for a serving tier
// whose healthy leaf latency is tens of milliseconds: enter probation above
// 200ms of smoothed latency, leave below 120ms.
func NewHealthTracker() *HealthTracker {
	return &HealthTracker{
		ewma:        map[NodeID]float64{},
		state:       map[NodeID]Health{},
		Alpha:       0.2,
		EnterMillis: 200,
		LeaveMillis: 120,
	}
}

// Observe records a latency sample for a node, updates its smoothed latency, and
// returns the resulting health state. The first sample for a node seeds the
// average directly so the tracker does not start every node from zero.
func (h *HealthTracker) Observe(node NodeID, latencyMillis float64) Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur, seen := h.ewma[node]
	if !seen {
		cur = latencyMillis
	} else {
		cur = h.Alpha*latencyMillis + (1-h.Alpha)*cur
	}
	h.ewma[node] = cur
	st := h.state[node]
	switch {
	case st == Healthy && cur > h.EnterMillis:
		st = Probation
	case st == Probation && cur < h.LeaveMillis:
		st = Healthy
	}
	h.state[node] = st
	return st
}

// State returns a node's current health, defaulting to Healthy for a node the
// tracker has not seen.
func (h *HealthTracker) State(node NodeID) Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state[node]
}

// InFanout reports whether the root should include a node in fan-out. A node on
// probation is excluded here; the caller still sends it shadow requests so the
// next Observe can return it to healthy.
func (h *HealthTracker) InFanout(node NodeID) bool {
	return h.State(node) != Probation
}
