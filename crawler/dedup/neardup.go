package dedup

// NearDupIndex answers "is there an already-seen fingerprint within Hamming
// distance 3 of this one?" in a few map probes instead of scanning the table
// (crawler doc 04.6). It uses the banded pigeonhole trick: split each 64-bit
// fingerprint into 4 blocks of 16 bits and key one table per block. If two
// fingerprints differ in at most 3 bits, those 3 differing bits touch at most 3
// of the 4 blocks, so at least one block is identical — and a lookup in that
// block's table will surface the candidate. Each candidate is then confirmed
// with a full Hamming check, so the scheme has no false positives and no false
// negatives at distance <= 3.
//
// The production form distributes these tables and persists them (the same DRUM
// machinery as the seen-set, 04.2); this in-memory index is the reference the
// scheme is tested against and is fine for a single shard's working set.
type NearDupIndex struct {
	// tables[b] maps the 16-bit value of block b to the cluster IDs whose
	// fingerprint has that block value.
	tables [numBlocks]map[uint16][]uint64
	// fp records each cluster's representative fingerprint for the verify step.
	fp map[uint64]uint64
}

const numBlocks = 4

// NewNearDupIndex returns an empty index.
func NewNearDupIndex() *NearDupIndex {
	idx := &NearDupIndex{fp: make(map[uint64]uint64)}
	for b := range idx.tables {
		idx.tables[b] = make(map[uint16][]uint64)
	}
	return idx
}

// block extracts the b-th 16-bit block of fp.
func block(fp uint64, b int) uint16 {
	return uint16(fp >> uint(b*16))
}

// Add inserts a fingerprint under the given cluster id.
func (x *NearDupIndex) Add(id, fp uint64) {
	x.fp[id] = fp
	for b := range numBlocks {
		key := block(fp, b)
		x.tables[b][key] = append(x.tables[b][key], id)
	}
}

// Query returns the id of an existing cluster whose fingerprint is within the
// near-duplicate threshold of fp, and whether one was found. When several match
// it returns the closest (smallest Hamming distance); ties break toward the
// smaller id for determinism.
func (x *NearDupIndex) Query(fp uint64) (uint64, bool) {
	bestID, bestDist, found := uint64(0), HammingThreshold+1, false
	seen := map[uint64]struct{}{}
	for b := range numBlocks {
		for _, id := range x.tables[b][block(fp, b)] {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			d := Hamming(fp, x.fp[id])
			if d <= HammingThreshold && (d < bestDist || (d == bestDist && id < bestID)) {
				bestID, bestDist, found = id, d, true
			}
		}
	}
	return bestID, found
}

// AddIfNew looks for a near-duplicate of fp; if one exists it returns that
// cluster's id and false (not new). Otherwise it inserts fp under newID and
// returns (newID, true). This is the crawler's per-document call: the returned
// id is the near-dup cluster id written to the WebTable meta family (04.6).
func (x *NearDupIndex) AddIfNew(newID, fp uint64) (clusterID uint64, isNew bool) {
	if id, ok := x.Query(fp); ok {
		return id, false
	}
	x.Add(newID, fp)
	return newID, true
}

// Len reports how many distinct clusters the index holds.
func (x *NearDupIndex) Len() int { return len(x.fp) }
