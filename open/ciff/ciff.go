// Package ciff exports a segment's lexical index in the Common Index File Format
// (implementation doc 11.1). CIFF is the Lucene/Anserini/PISA/Terrier interchange
// format and the lead open artifact, because it is the smallest useful unit a
// third party can independently load and verify: the M2 gate (doc 12) is that an
// outside CIFF-aware engine loads the export and reproduces OpenIndex's results.
//
// The exporter walks the segment in CIFF's required order (terms sorted
// lexicographically, postings sorted by ascending document id) and emits the
// three CIFF sections: a header with the collection statistics, the postings
// lists, and the document records. The encoding here is a self-describing,
// deterministic, length-prefixed binary that round-trips through Read, so the
// exporter and its tests are self-contained. The real CIFF protobuf wire format
// is the same logical structure in a fixed field order; swapping this encoder for
// the protobuf marshaler is the step that satisfies the M2 interop gate, and the
// Source seam and the section model do not change.
package ciff

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"iter"
	"math"
)

// magic tags the export so a reader rejects unrelated bytes, and version lets the
// format evolve without a silent misparse.
const (
	magic   = "CIFF"
	version = 1
)

// Header carries the collection statistics CIFF puts up front, which a loader
// needs before it reads a posting (the BM25 scorer wants the document count and
// the average length). NumPostingsLists and NumDocs let a reader size its
// structures and know when each section ends.
type Header struct {
	NumPostingsLists int
	NumDocs          int
	TotalTerms       int64 // sum of document lengths, the collection token count
	AverageDocLength float64
	Description      string
}

// Posting is one entry in a term's list: the internal document id and the term
// frequency in that document. Document ids are stored gap-encoded on the wire (a
// list sorted ascending compresses to small deltas), and decoded back to
// absolute ids by Read.
type Posting struct {
	DocID uint32
	TF    uint32
}

// PostingsList is one term's postings in CIFF order. DF is the document frequency
// (the number of postings) and CF the collection frequency (the sum of the term
// frequencies); a loader uses both for scoring without rescanning the list.
type PostingsList struct {
	Term     string
	DF       uint32
	CF       uint64
	Postings []Posting
}

// DocRecord is CIFF's per-document record: the internal id, the external
// collection id (the durable identity, a URL or content hash here), and the
// document length in terms.
type DocRecord struct {
	DocID           uint32
	CollectionDocID string
	Length          uint32
}

// Source yields a segment's contents in CIFF order. The exporter walks it once
// and never holds the whole index in memory, which is why Terms and Docs are
// iterators rather than slices. A production Source reads the segment's FST and
// postings store (doc 05); MemSource is the in-process reference.
type Source interface {
	Header() Header
	// Terms yields the postings lists in ascending term order, the order the
	// segment FST already walks.
	Terms() iter.Seq[PostingsList]
	// Docs yields the document records in ascending internal-id order.
	Docs() iter.Seq[DocRecord]
}

// Write exports a source to w in CIFF order. It streams: it reads each postings
// list and document record from the source once and writes it straight through,
// so the export costs no more memory than the largest single list. The byte
// stream is deterministic for a given source, so the export content-addresses
// stably (an artifact's identity is the hash of these bytes, doc 11.2).
func Write(w io.Writer, src Source) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString(magic); err != nil {
		return err
	}
	if err := putUvarint(bw, version); err != nil {
		return err
	}
	h := src.Header()
	if err := writeHeader(bw, h); err != nil {
		return err
	}
	lists := 0
	for pl := range src.Terms() {
		if err := writePostingsList(bw, pl); err != nil {
			return err
		}
		lists++
	}
	if lists != h.NumPostingsLists {
		return fmt.Errorf("ciff: header says %d postings lists, source yielded %d", h.NumPostingsLists, lists)
	}
	docs := 0
	for dr := range src.Docs() {
		if err := writeDocRecord(bw, dr); err != nil {
			return err
		}
		docs++
	}
	if docs != h.NumDocs {
		return fmt.Errorf("ciff: header says %d docs, source yielded %d", h.NumDocs, docs)
	}
	return bw.Flush()
}

func writeHeader(w *bufio.Writer, h Header) error {
	if err := putUvarint(w, uint64(h.NumPostingsLists)); err != nil {
		return err
	}
	if err := putUvarint(w, uint64(h.NumDocs)); err != nil {
		return err
	}
	if err := putUvarint(w, uint64(h.TotalTerms)); err != nil {
		return err
	}
	if err := putUvarint(w, math64bits(h.AverageDocLength)); err != nil {
		return err
	}
	return putString(w, h.Description)
}

func writePostingsList(w *bufio.Writer, pl PostingsList) error {
	if err := putString(w, pl.Term); err != nil {
		return err
	}
	if err := putUvarint(w, uint64(pl.DF)); err != nil {
		return err
	}
	if err := putUvarint(w, pl.CF); err != nil {
		return err
	}
	if err := putUvarint(w, uint64(len(pl.Postings))); err != nil {
		return err
	}
	var prev uint32
	for i, p := range pl.Postings {
		if i > 0 && p.DocID < prev {
			return fmt.Errorf("ciff: term %q postings not sorted by docid", pl.Term)
		}
		if err := putUvarint(w, uint64(p.DocID-prev)); err != nil { // gap encoding
			return err
		}
		if err := putUvarint(w, uint64(p.TF)); err != nil {
			return err
		}
		prev = p.DocID
	}
	return nil
}

func writeDocRecord(w *bufio.Writer, dr DocRecord) error {
	if err := putUvarint(w, uint64(dr.DocID)); err != nil {
		return err
	}
	if err := putString(w, dr.CollectionDocID); err != nil {
		return err
	}
	return putUvarint(w, uint64(dr.Length))
}

// Index is a fully decoded CIFF export, the shape Read returns. A loader (or a
// test verifying the export) gets the header and the two sections back as slices.
type Index struct {
	Header Header
	Terms  []PostingsList
	Docs   []DocRecord
}

// Read decodes a CIFF export written by Write. It is the verification half: a
// consumer reads the export, and a test confirms it round-trips, so the encoder
// and decoder agree on the format before either is swapped for the protobuf.
func Read(r io.Reader) (*Index, error) {
	br := bufio.NewReader(r)
	buf := make([]byte, len(magic))
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	if string(buf) != magic {
		return nil, fmt.Errorf("ciff: bad magic %q", buf)
	}
	v, err := binary.ReadUvarint(br)
	if err != nil {
		return nil, err
	}
	if v != version {
		return nil, fmt.Errorf("ciff: unsupported version %d", v)
	}
	idx := &Index{}
	if idx.Header, err = readHeader(br); err != nil {
		return nil, err
	}
	idx.Terms = make([]PostingsList, idx.Header.NumPostingsLists)
	for i := range idx.Terms {
		if idx.Terms[i], err = readPostingsList(br); err != nil {
			return nil, err
		}
	}
	idx.Docs = make([]DocRecord, idx.Header.NumDocs)
	for i := range idx.Docs {
		if idx.Docs[i], err = readDocRecord(br); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

func readHeader(r *bufio.Reader) (Header, error) {
	var h Header
	npl, err := binary.ReadUvarint(r)
	if err != nil {
		return h, err
	}
	nd, err := binary.ReadUvarint(r)
	if err != nil {
		return h, err
	}
	tt, err := binary.ReadUvarint(r)
	if err != nil {
		return h, err
	}
	adl, err := binary.ReadUvarint(r)
	if err != nil {
		return h, err
	}
	desc, err := readString(r)
	if err != nil {
		return h, err
	}
	h.NumPostingsLists = int(npl)
	h.NumDocs = int(nd)
	h.TotalTerms = int64(tt)
	h.AverageDocLength = math64float(adl)
	h.Description = desc
	return h, nil
}

func readPostingsList(r *bufio.Reader) (PostingsList, error) {
	var pl PostingsList
	term, err := readString(r)
	if err != nil {
		return pl, err
	}
	df, err := binary.ReadUvarint(r)
	if err != nil {
		return pl, err
	}
	cf, err := binary.ReadUvarint(r)
	if err != nil {
		return pl, err
	}
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return pl, err
	}
	pl.Term = term
	pl.DF = uint32(df)
	pl.CF = cf
	pl.Postings = make([]Posting, n)
	var abs uint32
	for i := range pl.Postings {
		gap, err := binary.ReadUvarint(r)
		if err != nil {
			return pl, err
		}
		tf, err := binary.ReadUvarint(r)
		if err != nil {
			return pl, err
		}
		abs += uint32(gap)
		pl.Postings[i] = Posting{DocID: abs, TF: uint32(tf)}
	}
	return pl, nil
}

func readDocRecord(r *bufio.Reader) (DocRecord, error) {
	var dr DocRecord
	id, err := binary.ReadUvarint(r)
	if err != nil {
		return dr, err
	}
	cid, err := readString(r)
	if err != nil {
		return dr, err
	}
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return dr, err
	}
	dr.DocID = uint32(id)
	dr.CollectionDocID = cid
	dr.Length = uint32(length)
	return dr, nil
}

func putUvarint(w *bufio.Writer, x uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], x)
	_, err := w.Write(buf[:n])
	return err
}

func putString(w *bufio.Writer, s string) error {
	if err := putUvarint(w, uint64(len(s))); err != nil {
		return err
	}
	_, err := w.WriteString(s)
	return err
}

func readString(r *bufio.Reader) (string, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// math64bits and math64float carry the average document length as its IEEE-754
// bit pattern through a uvarint, so the float round-trips exactly rather than
// through a lossy decimal string.
func math64bits(f float64) uint64  { return math.Float64bits(f) }
func math64float(u uint64) float64 { return math.Float64frombits(u) }
