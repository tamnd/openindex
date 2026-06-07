// Package openindex holds the primitive domain types shared across every
// subsystem of the engine. It is a leaf: it imports nothing else in the module,
// so storage, index, rank, serve, and the rest may all depend on it without
// creating a cycle (impl spec 02.3).
//
// The wire types live in package proto; these are the in-process domain types
// that each subsystem converts to at its edge (impl spec 02.6). Keeping the
// shared vocabulary — what a document id is, what a snapshot is named, how a
// score compares — in one place stops every package from inventing its own.
package openindex

import "fmt"

// DocID is a per-segment sequential document identifier. It is dense within a
// segment so it can index directly into columnar arrays, and it is reassigned
// on merge (impl spec 05). A DocID is meaningless outside the segment that
// minted it; the durable external identity of a page is its URL and ContentHash.
type DocID uint32

// GlobalDocID pairs a segment with a local DocID to address a document across
// the whole index. The serving tree carries these only within a query's
// lifetime (impl spec 02.4).
type GlobalDocID struct {
	Segment SegmentID
	Local   DocID
}

// SegmentID names an immutable index segment. Segments are never mutated in
// place; a merge produces a new SegmentID and retires its inputs (impl spec 05).
type SegmentID uint64

// TermID is the dense identifier a term receives within a segment's term
// dictionary. The FST maps term bytes to TermID; the postings store is keyed by
// TermID (impl spec 05).
type TermID uint32

// Score is a relevance score. Scores are only ever compared within a single
// result set; their absolute magnitude is not portable across rankers or
// segments (impl spec 07).
type Score float32

// Less reports whether s should rank below other. Higher scores rank first, so
// Less is the reverse of the numeric order — it is the comparator a min-heap of
// the top-k results wants.
func (s Score) Less(other Score) bool { return s > other }

// SnapshotID names an immutable, published index snapshot. It is the unit of
// reproducibility for the open index: a snapshot id plus the public pipeline
// config fully determines the artifact bytes (spec 10, impl spec 11). It also
// travels in every search request so a leaf can reject a format it cannot read
// (impl spec 02.4).
type SnapshotID string

// ContentHash is the content address of a fetched resource — the durable,
// dedup-friendly identity of bytes on the web, independent of the URL they were
// served from (impl spec 03, 04).
type ContentHash [32]byte

// String renders the hash as lowercase hex, the form used in WARC records,
// shard manifests, and log lines.
func (h ContentHash) String() string {
	const hex = "0123456789abcdef"
	var b [64]byte
	for i, c := range h {
		b[i*2] = hex[c>>4]
		b[i*2+1] = hex[c&0x0f]
	}
	return string(b[:])
}

// Result is one scored, addressable hit returned up the serving tree. It is the
// lowest common denominator the leaf produces and the mixer consumes (impl spec
// 08); snippet text and citation spans are attached later in the pipeline.
type Result struct {
	Doc   GlobalDocID
	URL   string
	Score Score
}

// Posting is one entry in a term's posting list: the document the term occurs
// in and how many times. Positions, when carried, live in a separate parallel
// stream so the document-frequency scan never pays to decode them (impl spec
// 05).
type Posting struct {
	Doc       DocID
	Frequency uint32
}

// Field names a logical text field of a document for BM25F-style weighted
// scoring (impl spec 07). The set is fixed at index time so a field maps to a
// stable small integer in the segment.
type Field uint8

const (
	FieldBody Field = iota
	FieldTitle
	FieldAnchor // inlink anchor text aggregated from the link graph
	FieldURL
	numFields
)

// String returns the canonical lowercase field name used in config and logs.
func (f Field) String() string {
	switch f {
	case FieldBody:
		return "body"
	case FieldTitle:
		return "title"
	case FieldAnchor:
		return "anchor"
	case FieldURL:
		return "url"
	default:
		return fmt.Sprintf("field(%d)", uint8(f))
	}
}

// NumFields is the count of scorable fields, sized for fixed-length per-field
// arrays in the forward store and the BM25F field-weight vector.
const NumFields = int(numFields)
