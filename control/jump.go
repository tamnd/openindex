// Jump consistent hashing for document-to-shard assignment (architecture doc
// 10.3). It is Google's algorithm: an O(1), allocation-free function from a key
// to a bucket in [0, numBuckets) that moves only the minimal share of keys when
// the bucket count grows. That last property is the point: when the index gains
// shards, almost every document stays where it was, so resharding does not
// reshuffle the whole corpus.
//
// The one limitation is that jump hashing supports only an append-only bucket
// list: it can grow the count but cannot remove an arbitrary bucket. The
// control plane handles that with the indirection in doc 10.3, a stable jump
// bucket count mapped to physical shards, so the bucket count never shrinks and
// physical placement stays flexible.

package control

// JumpHash maps key to a bucket in [0, numBuckets) using jump consistent
// hashing. numBuckets must be at least 1. The result depends only on key and
// numBuckets, so it is a pure function: the same key lands in the same bucket on
// every node without any shared state.
func JumpHash(key uint64, numBuckets int) int {
	if numBuckets < 1 {
		numBuckets = 1
	}
	var b, j int64 = -1, 0
	for j < int64(numBuckets) {
		b = j
		// A linear-congruential step over the key, the constants from the paper.
		key = key*2862933555777941757 + 1
		// The next jump is the inverse of a uniform draw, which spaces the jumps
		// so each bucket ends up with a near-equal share of keys.
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int(b)
}
