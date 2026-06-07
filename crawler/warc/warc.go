// Package warc writes the crawl corpus as standard WARC 1.1 with per-record
// gzip members (crawler doc 04.8). This is the open corpus (architecture doc
// 10), so it is the same format Common Crawl publishes: each fetch event yields
// a request, a response, and a metadata record.
//
// Each record is an independent gzip member, so the concatenation is still a
// valid gzip stream and any single record can be extracted by
// (file, byte offset, compressed length) without inflating the whole file -
// exactly what an answer-engine citation needs to resolve to an archived
// artifact (doc 09). Production read/write uses internetarchive/gowarc; this is
// the dependency-free writer the format is tested against.
package warc

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/base32"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RecordType is the WARC-Type of a record.
type RecordType string

const (
	TypeRequest  RecordType = "request"
	TypeResponse RecordType = "response"
	TypeMetadata RecordType = "metadata"
)

// Locator addresses one record inside a WARC file: the file-relative byte
// offset of its gzip member and the member's compressed length. The open index
// stores these so a citation resolves to an exact archived record (04.8).
type Locator struct {
	Offset int64
	Length int64
}

// Writer appends gzip-membered WARC records to an underlying stream, tracking
// each record's byte offset. It is not safe for concurrent use; one Writer
// serves one output file.
type Writer struct {
	w       *countingWriter
	now     func() time.Time
	idSeq   uint64
	idStem  string // stable per-writer stem for record IDs
	records int
}

// Option configures a Writer.
type Option func(*Writer)

// WithClock overrides the WARC-Date source, for deterministic tests.
func WithClock(now func() time.Time) Option { return func(w *Writer) { w.now = now } }

// WithIDStem sets the stable stem used to mint record IDs, for deterministic
// tests; real writers leave it empty and a random stem is used.
func WithIDStem(stem string) Option { return func(w *Writer) { w.idStem = stem } }

// NewWriter wraps dst.
func NewWriter(dst io.Writer, opts ...Option) *Writer {
	w := &Writer{
		w:      &countingWriter{w: dst},
		now:    time.Now,
		idStem: "openindex",
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Record is one WARC record to write.
type Record struct {
	Type      RecordType
	TargetURI string
	// ContentType is the WARC Content-Type of the block, e.g.
	// "application/http;msgtype=response" for a response record.
	ContentType string
	// Fields are extra WARC header fields (e.g. WARC-Identified-Payload-Type,
	// near-dup cluster, consent state) written in sorted order for determinism.
	Fields map[string]string
	// Block is the record payload (for a response: the HTTP status line,
	// headers, and body).
	Block []byte
}

// Write serializes rec as one independent gzip member and returns its Locator.
func (w *Writer) Write(rec Record) (Locator, error) {
	start := w.w.n

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "WARC/1.1\r\n")
	fmt.Fprintf(&buf, "WARC-Type: %s\r\n", rec.Type)
	fmt.Fprintf(&buf, "WARC-Date: %s\r\n", w.now().UTC().Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(&buf, "WARC-Record-ID: <urn:uuid:%s>\r\n", w.nextID())
	if rec.TargetURI != "" {
		fmt.Fprintf(&buf, "WARC-Target-URI: %s\r\n", rec.TargetURI)
	}
	if len(rec.Block) > 0 {
		fmt.Fprintf(&buf, "WARC-Block-Digest: sha1:%s\r\n", sha1Base32(rec.Block))
	}
	ct := rec.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	fmt.Fprintf(&buf, "Content-Type: %s\r\n", ct)
	for _, k := range sortedKeys(rec.Fields) {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, rec.Fields[k])
	}
	// Content-Length is the block length; the header block ends with a blank
	// line and the record ends with two CRLFs, per the WARC grammar.
	fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(rec.Block))
	buf.WriteString("\r\n")
	buf.Write(rec.Block)
	buf.WriteString("\r\n\r\n")

	gz := gzip.NewWriter(w.w)
	if _, err := gz.Write(buf.Bytes()); err != nil {
		_ = gz.Close()
		return Locator{}, fmt.Errorf("warc: write record block: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Locator{}, fmt.Errorf("warc: close gzip member: %w", err)
	}
	w.records++
	return Locator{Offset: start, Length: w.w.n - start}, nil
}

// WriteResponse writes a response record carrying the raw HTTP response bytes
// (status line, headers, body).
func (w *Writer) WriteResponse(uri string, httpResponse []byte, fields map[string]string) (Locator, error) {
	return w.Write(Record{
		Type:        TypeResponse,
		TargetURI:   uri,
		ContentType: "application/http;msgtype=response",
		Fields:      fields,
		Block:       httpResponse,
	})
}

// WriteRequest writes a request record carrying the raw HTTP request bytes.
func (w *Writer) WriteRequest(uri string, httpRequest []byte) (Locator, error) {
	return w.Write(Record{
		Type:        TypeRequest,
		TargetURI:   uri,
		ContentType: "application/http;msgtype=request",
		Block:       httpRequest,
	})
}

// WriteMetadata writes a metadata record as a warc-fields block (sorted
// key: value lines): detected language, charset, near-dup cluster, consent
// state, and so on (04.8).
func (w *Writer) WriteMetadata(uri string, fields map[string]string) (Locator, error) {
	var b strings.Builder
	for _, k := range sortedKeys(fields) {
		fmt.Fprintf(&b, "%s: %s\r\n", k, fields[k])
	}
	return w.Write(Record{
		Type:        TypeMetadata,
		TargetURI:   uri,
		ContentType: "application/warc-fields",
		Block:       []byte(b.String()),
	})
}

// Records returns how many records have been written.
func (w *Writer) Records() int { return w.records }

func (w *Writer) nextID() string {
	w.idSeq++
	return fmt.Sprintf("%s-%012d", w.idStem, w.idSeq)
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sha1Base32(b []byte) string {
	sum := sha1.Sum(b)
	return base32.StdEncoding.EncodeToString(sum[:])
}

// countingWriter counts bytes passed through so record offsets are known.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// parseContentLength is a small helper kept here so the writer and a reader test
// share one understanding of the header.
func parseContentLength(headerLine string) (int, bool) {
	const prefix = "Content-Length:"
	if !strings.HasPrefix(headerLine, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(headerLine[len(prefix):]))
	if err != nil {
		return 0, false
	}
	return n, true
}
