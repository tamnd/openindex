// Package forward is the forward store (indexer doc 05.5): the per-document
// fields snippet generation needs after ranking — title, URL, body, and the
// field norms BM25F scores against. Following Lucene's stored-fields format,
// documents are grouped into compressed chunks with a monotonic block-offset
// index (first doc id and byte offset per chunk), so a document is reached by a
// binary search over chunks followed by one chunk decompress, never a scan.
//
// Compression is a seam: the hot tier wants fast decode (Snappy/none), the cold
// tier wants ratio (zstd), and doc 05.5 calls the higher-ratio option a
// fork-level addition rather than a flag. NewStore takes a Codec so the tier
// chooses; the reference store ships an identity codec so the layout is tested
// without pulling a compression dependency into the module.
package forward

import (
	"encoding/binary"
	"errors"
	"sort"

	"openindex"
)

// Document is the stored form of one indexed document.
type Document struct {
	URL   string
	Title string
	Body  string
	// Norms is the per-field length norm BM25F reads (doc 07); fixed length so
	// it indexes by Field directly.
	Norms [openindex.NumFields]uint16
}

// Codec compresses and decompresses a chunk payload. Identity is the reference;
// Snappy/zstd implement the same interface for the hot/cold tiers (doc 05.5).
type Codec interface {
	Compress(dst, src []byte) []byte
	Decompress(dst, src []byte) ([]byte, error)
	Name() string
}

// Identity is the no-op codec used by the reference store and tests.
type Identity struct{}

func (Identity) Compress(dst, src []byte) []byte { return append(dst, src...) }
func (Identity) Decompress(dst, src []byte) ([]byte, error) {
	return append(dst, src...), nil
}
func (Identity) Name() string { return "identity" }

// chunkRef is the monotonic offset-index entry for one chunk.
type chunkRef struct {
	firstDoc openindex.DocID
	offset   int // byte offset of the chunk in data
	rawLen   int // decompressed length, so Decompress can size its buffer
	count    int // documents in the chunk
}

// Store is an immutable forward store: compressed chunks plus the chunk index.
type Store struct {
	codec  Codec
	data   []byte
	chunks []chunkRef
}

// Writer accumulates documents in ascending DocID order and seals a Store.
// Documents are buffered into chunks of chunkDocs and flushed compressed.
type Writer struct {
	codec     Codec
	chunkDocs int
	data      []byte
	chunks    []chunkRef
	buf       []byte
	bufFirst  openindex.DocID
	bufCount  int
	nextDoc   openindex.DocID
	started   bool
}

// DefaultChunkDocs is the document count per stored-fields chunk. Small enough
// that decompressing a chunk to read one document is cheap, large enough that
// the per-chunk index stays small (doc 05.5).
const DefaultChunkDocs = 16

// NewWriter returns a writer using codec and the default chunk size.
func NewWriter(codec Codec) *Writer {
	return &Writer{codec: codec, chunkDocs: DefaultChunkDocs}
}

// Add appends doc under id. Segment doc IDs are internal, dense, and sequential
// (doc 02, 05.6), so a chunk's position math is id-minus-firstDoc; the writer
// enforces that contract by requiring each id to follow the previous one. The
// first id may be any value (a segment's id base), and the rest must be
// contiguous.
func (w *Writer) Add(id openindex.DocID, doc Document) error {
	if w.started && id != w.nextDoc {
		return errors.New("forward: segment doc IDs must be dense and sequential")
	}
	w.started = true
	w.nextDoc = id + 1
	if w.bufCount == 0 {
		w.bufFirst = id
	}
	w.buf = appendDoc(w.buf, doc)
	w.bufCount++
	if w.bufCount >= w.chunkDocs {
		w.flush()
	}
	return nil
}

func (w *Writer) flush() {
	if w.bufCount == 0 {
		return
	}
	ref := chunkRef{
		firstDoc: w.bufFirst,
		offset:   len(w.data),
		rawLen:   len(w.buf),
		count:    w.bufCount,
	}
	w.data = w.codec.Compress(w.data, w.buf)
	w.chunks = append(w.chunks, ref)
	w.buf = w.buf[:0]
	w.bufCount = 0
}

// Seal flushes the final chunk and returns the immutable Store.
func (w *Writer) Seal() *Store {
	w.flush()
	return &Store{codec: w.codec, data: w.data, chunks: w.chunks}
}

// Get returns the document stored under id. It binary-searches the chunk index
// for the chunk whose range covers id, decompresses that one chunk, and decodes
// the id-th document within it.
func (s *Store) Get(id openindex.DocID) (Document, bool) {
	// Find the last chunk whose firstDoc <= id.
	i := sort.Search(len(s.chunks), func(i int) bool { return s.chunks[i].firstDoc > id })
	if i == 0 {
		return Document{}, false
	}
	ref := s.chunks[i-1]
	if id >= ref.firstDoc+openindex.DocID(ref.count) {
		return Document{}, false // id falls in the gap past this chunk's docs
	}
	end := len(s.data)
	if i < len(s.chunks) {
		end = s.chunks[i].offset
	}
	raw, err := s.codec.Decompress(make([]byte, 0, ref.rawLen), s.data[ref.offset:end])
	if err != nil {
		return Document{}, false
	}
	want := int(id - ref.firstDoc)
	doc, ok := decodeNthDoc(raw, want)
	return doc, ok
}

// Len reports how many documents the store holds.
func (s *Store) Len() int {
	var n int
	for _, c := range s.chunks {
		n += c.count
	}
	return n
}

// appendDoc serializes one document: uvarint-length-prefixed URL/Title/Body
// followed by the fixed norm array.
func appendDoc(dst []byte, d Document) []byte {
	dst = appendStr(dst, d.URL)
	dst = appendStr(dst, d.Title)
	dst = appendStr(dst, d.Body)
	for _, n := range d.Norms {
		dst = binary.AppendUvarint(dst, uint64(n))
	}
	return dst
}

// decodeNthDoc decodes the doc at index n within a chunk payload.
func decodeNthDoc(buf []byte, n int) (Document, bool) {
	pos := 0
	var d Document
	for i := 0; ; i++ {
		url, p1 := readStr(buf[pos:])
		title, p2 := readStr(buf[pos+p1:])
		body, p3 := readStr(buf[pos+p1+p2:])
		off := pos + p1 + p2 + p3
		var norms [openindex.NumFields]uint16
		for f := range norms {
			v, m := binary.Uvarint(buf[off:])
			norms[f] = uint16(v)
			off += m
		}
		if i == n {
			d.URL, d.Title, d.Body, d.Norms = url, title, body, norms
			return d, true
		}
		pos = off
		if pos >= len(buf) {
			return Document{}, false
		}
	}
}

func appendStr(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func readStr(src []byte) (string, int) {
	n, m := binary.Uvarint(src)
	return string(src[m : m+int(n)]), m + int(n)
}
