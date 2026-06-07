package segment

import "sort"

// MergePolicy decides which segments to fold together. It is tiered, starting
// from Lucene's tuned defaults (doc 05.6): small segments are merged into a
// logarithmic staircase, tiny segments are floored so they do not form a long
// tail, and a maximum merged size caps the cost of any single merge. Merging is
// I/O-intensive and must be rate-limited by the caller so it never starves
// serving (doc 08); this type only chooses the candidates.
type MergePolicy struct {
	// SegmentsPerTier is how many segments of roughly equal size accumulate
	// before they are eligible to merge (Lucene default 10).
	SegmentsPerTier int
	// MaxMergeAtOnce caps how many segments one merge consumes (default 10).
	MaxMergeAtOnce int
	// FloorSegmentDocs rounds tiny segments up to one size class so a long tail
	// of single-doc segments does not defeat tiering (Lucene floors by MB; the
	// reference floors by doc count).
	FloorSegmentDocs int
	// MaxMergedDocs caps the document count of a produced segment (Lucene's 5 GB
	// cap, expressed here in docs).
	MaxMergedDocs int
	// DeletesPctAllowed triggers a merge of a segment whose deleted fraction
	// exceeds this percentage even if tiering would not (default 33).
	DeletesPctAllowed int
}

// DefaultMergePolicy returns Lucene's tuned tiered defaults (doc 05.6).
func DefaultMergePolicy() MergePolicy {
	return MergePolicy{
		SegmentsPerTier:   10,
		MaxMergeAtOnce:    10,
		FloorSegmentDocs:  2000,
		MaxMergedDocs:     50_000_000,
		DeletesPctAllowed: 33,
	}
}

// flooredSize is a segment's size class for tiering: its live-doc count rounded
// up to the floor, so segments below the floor compare equal.
func (p MergePolicy) flooredSize(s *Segment) int {
	n := s.LiveDocs()
	if n < p.FloorSegmentDocs {
		return p.FloorSegmentDocs
	}
	return n
}

// tooManyDeletes reports whether a segment's deleted fraction has crossed the
// reclaim threshold, which makes it a merge candidate on its own.
func (p MergePolicy) tooManyDeletes(s *Segment) bool {
	if s.numDocs == 0 {
		return false
	}
	deleted := s.numDocs - s.LiveDocs()
	return deleted*100 >= s.numDocs*p.DeletesPctAllowed
}

// Select chooses the next batch of segments to merge, or nil if none qualify.
// It mirrors tiered selection: sort by size, and if the smallest tier has
// accumulated at least SegmentsPerTier segments, merge up to MaxMergeAtOnce of
// the smallest ones (bounded by MaxMergedDocs). A segment over the delete
// threshold is selected for a singleton rewrite that reclaims its deletions.
func (p MergePolicy) Select(segs []*Segment) []*Segment {
	if len(segs) == 0 {
		return nil
	}
	// Delete-driven rewrite takes priority: it reclaims space a tiering pass
	// would otherwise leave stranded in a large segment.
	for _, s := range segs {
		if p.tooManyDeletes(s) {
			return []*Segment{s}
		}
	}

	sorted := make([]*Segment, len(segs))
	copy(sorted, segs)
	sort.Slice(sorted, func(i, j int) bool {
		return p.flooredSize(sorted[i]) < p.flooredSize(sorted[j])
	})

	if len(sorted) < p.SegmentsPerTier {
		return nil
	}
	// Merge the smallest run, bounded by count and total docs.
	var batch []*Segment
	var total int
	for _, s := range sorted {
		if len(batch) >= p.MaxMergeAtOnce {
			break
		}
		if total+s.LiveDocs() > p.MaxMergedDocs && len(batch) > 0 {
			break
		}
		batch = append(batch, s)
		total += s.LiveDocs()
	}
	if len(batch) < 2 {
		return nil
	}
	return batch
}
