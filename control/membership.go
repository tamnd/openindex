// Node membership over the coordination store (architecture doc 10.2). A serving
// node registers under a lease it keepalives; the registration key lives only as
// long as the lease, so a node that dies (stops keepaliving) has its key deleted
// by the store, and the placement controller sees the node leave in one watch
// event. This is the etcd-lease analog of Bigtable's Chubby lock: the lease is
// the node's claim on being alive, and losing it frees the node's shards.

package control

import (
	"context"
	"slices"
	"strings"
)

// nodePrefix is the store key prefix node registrations live under. The
// placement controller lists and watches this prefix to learn the live set.
const nodePrefix = "node/"

func nodeKey(id NodeID) string {
	return nodePrefix + string(id)
}

// Register records a node as live by writing its key under the given lease. The
// caller obtained the lease from Grant and keepalives it; when keepalives stop,
// the store deletes the key and the node leaves the membership. The value is the
// node id, which keeps the registration self-describing without a separate read.
func Register(ctx context.Context, store Store, id NodeID, lease LeaseID) error {
	return store.Put(ctx, nodeKey(id), []byte(id), lease)
}

// Members returns the live node set, sorted, so two readers see the same order
// and any placement computed from it is reproducible.
func Members(ctx context.Context, store Store) ([]NodeID, error) {
	kvs, err := store.List(ctx, nodePrefix)
	if err != nil {
		return nil, err
	}
	out := make([]NodeID, 0, len(kvs))
	for k := range kvs {
		id, ok := strings.CutPrefix(k, nodePrefix)
		if !ok {
			continue
		}
		out = append(out, NodeID(id))
	}
	slices.Sort(out)
	return out, nil
}
