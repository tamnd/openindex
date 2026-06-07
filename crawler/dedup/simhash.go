// Package dedup detects duplicate and near-duplicate pages (crawler doc 04.6).
// Exact duplicates are caught by a content hash a cryptographic digest can see;
// near-duplicates - boilerplate-heavy pages that differ in a sentence - need
// Charikar's 64-bit SimHash, whose fingerprint is 8 bytes (versus 24 for Broder
// shingles), which is what lets a billion-document table fit in memory.
//
// Two documents are near-duplicates if their fingerprints differ in at most 3
// of 64 bits. The NearDupIndex answers that query without the 41,664-probe scan
// a naive Hamming search would do, using the banded permuted-table scheme of
// 04.6.
package dedup

import (
	"crypto/sha256"
	"hash/fnv"
	"math/bits"
	"strings"

	"openindex"
)

// HammingThreshold is the bit-distance at or below which two SimHash
// fingerprints are considered near-duplicates (04.6).
const HammingThreshold = 3

// ContentHash returns the exact content address of a page body - the cheap
// first-line dedup that catches byte-identical copies before SimHash is needed.
func ContentHash(body []byte) openindex.ContentHash {
	return sha256.Sum256(body)
}

// SimHash computes the 64-bit Charikar fingerprint of a bag of weighted
// features. Each feature is hashed to 64 bits; for every bit position the
// feature's weight is added when the bit is set and subtracted when it is
// clear; the fingerprint bit is the sign of the resulting accumulator. Documents
// that share most features land on the same side of most accumulators and so get
// close fingerprints.
func SimHash(features map[string]int) uint64 {
	var acc [64]int64
	for feat, w := range features {
		h := hashFeature(feat)
		weight := int64(w)
		for i := range 64 {
			if h&(1<<uint(i)) != 0 {
				acc[i] += weight
			} else {
				acc[i] -= weight
			}
		}
	}
	var fp uint64
	for i := range 64 {
		if acc[i] > 0 {
			fp |= 1 << uint(i)
		}
	}
	return fp
}

// SimHashText is the convenience path: it shingles text into overlapping
// k-word features weighted by frequency and SimHashes them. Shingling rather
// than single words is what makes the fingerprint sensitive to word order, so
// two pages with the same vocabulary in different arrangements are not collapsed.
func SimHashText(text string, k int) uint64 {
	return SimHash(Shingle(text, k))
}

// Shingle splits text into overlapping k-word shingles and returns their
// frequencies. Tokenization is lowercase whitespace splitting, which is enough
// for the dedup signal; the indexer's analyzer (doc 05) is the authority on real
// tokenization.
func Shingle(text string, k int) map[string]int {
	if k < 1 {
		k = 1
	}
	words := strings.Fields(strings.ToLower(text))
	out := make(map[string]int)
	if len(words) < k {
		if len(words) > 0 {
			out[strings.Join(words, " ")]++
		}
		return out
	}
	for i := 0; i+k <= len(words); i++ {
		out[strings.Join(words[i:i+k], " ")]++
	}
	return out
}

// Hamming returns the number of differing bits between two fingerprints.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// NearDup reports whether two fingerprints are within the near-duplicate
// threshold.
func NearDup(a, b uint64) bool { return Hamming(a, b) <= HammingThreshold }

func hashFeature(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
