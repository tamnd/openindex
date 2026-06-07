package segment

import "math/bits"

// bitset is a compact live-docs set: bit i set means document i is live. Deletes
// clear a bit; space is reclaimed only on merge, the Lucene/Scorch model (doc
// 05.6). It is not safe for concurrent mutation, but reads are safe once the
// segment is sealed.
type bitset struct {
	words []uint64
	n     int // number of bits
}

func newBitset(n int) *bitset {
	return &bitset{words: make([]uint64, (n+63)/64), n: n}
}

// setAll marks every bit live, the initial state of a freshly built segment.
func (b *bitset) setAll() {
	for i := range b.words {
		b.words[i] = ^uint64(0)
	}
	// Clear the padding bits past n so popcount is exact.
	if rem := b.n % 64; rem != 0 {
		b.words[len(b.words)-1] = uint64(1)<<uint(rem) - 1
	}
}

func (b *bitset) get(i int) bool {
	if i < 0 || i >= b.n {
		return false
	}
	return b.words[i>>6]&(1<<uint(i&63)) != 0
}

func (b *bitset) clear(i int) {
	if i < 0 || i >= b.n {
		return
	}
	b.words[i>>6] &^= 1 << uint(i&63)
}

func (b *bitset) count() int {
	var c int
	for _, w := range b.words {
		c += bits.OnesCount64(w)
	}
	return c
}
