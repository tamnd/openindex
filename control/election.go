// Leader election for the singleton roles (architecture doc 10.3). The placement
// controller and the merge scheduler must run as exactly one instance, or two of
// them would publish conflicting assignments. Election rides on the store: a
// candidate claims the leader key under its lease, and the store's atomic create
// lets exactly one win. The winner holds leadership only as long as its lease is
// alive, so a leader that dies (lease expiry deletes the key) yields to the next
// candidate without a human in the loop.

package control

import "context"

// leaderPrefix is the store key prefix leader keys live under. Each singleton
// role gets one key, e.g. "leader/placement".
const leaderPrefix = "leader/"

func leaderKey(role string) string {
	return leaderPrefix + role
}

// Campaign attempts to become the leader for a role by claiming its key under
// the candidate's lease. It returns true if this candidate won. Because the
// claim is an atomic create, exactly one candidate wins; the others see the key
// taken and return false. The winner stays leader until it Resigns or its lease
// expires, at which point the key vanishes and the next Campaign can win.
func Campaign(ctx context.Context, store Store, role string, id NodeID, lease LeaseID) (bool, error) {
	return store.Create(ctx, leaderKey(role), []byte(id), lease)
}

// Resign gives up leadership by deleting the leader key, letting a waiting
// candidate win immediately rather than after a lease timeout. A leader that
// crashes instead of resigning is covered by the lease expiry.
func Resign(ctx context.Context, store Store, role string) error {
	return store.Delete(ctx, leaderKey(role))
}

// Leader returns the current leader for a role and whether the role is held. A
// reader uses it to find who is in charge without campaigning.
func Leader(ctx context.Context, store Store, role string) (NodeID, bool, error) {
	v, ok, err := store.Get(ctx, leaderKey(role))
	if err != nil || !ok {
		return "", false, err
	}
	return NodeID(v), true, nil
}
