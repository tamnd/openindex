package storage

import (
	"encoding/binary"
	"strings"
)

// WebTable is the Bigtable-style document store (storage doc 03.2): a sparse,
// multi-version, column-family map over the crawled web, keyed for crawl and
// link locality. It is a thin schema layer over an Engine — the key layout is
// the design, the storage is pluggable.
//
// Row key is the reversed hostname followed by the path, so every page of a
// site and every site of a domain is contiguous and per-host / per-domain scans
// (politeness, site quality, link inversion) are sequential rather than
// scattered. Cells are versioned by crawl timestamp, newest first, so a read
// without a timestamp returns the latest observation and the freshness history
// stays queryable.
type WebTable struct {
	eng Engine
}

// NewWebTable wraps an Engine as a WebTable.
func NewWebTable(eng Engine) *WebTable { return &WebTable{eng: eng} }

// Family is a column family: a separately-tunable region of a row. The byte
// value orders families within a row.
type Family byte

const (
	// FamilyContents holds the fetched payload, multi-versioned by crawl time.
	// Large bodies spill to the blob store with the cell holding a value-pointer.
	FamilyContents Family = iota
	// FamilyAnchor holds inbound anchor text keyed by source URL, the
	// high-weight BM25F field; written by the link-inversion pass, not the
	// crawler (03.2).
	FamilyAnchor
	// FamilyMeta holds fetch status, content type, language, near-dup cluster
	// id, robots/consent state, and the quality/PageRank score written back by
	// the batch pipeline.
	FamilyMeta
)

// ReverseHost turns a hostname into its dot-reversed form, e.g.
// "www.example.com" -> "com.example.www", so domain locality becomes key-prefix
// locality. An empty host yields an empty string.
func ReverseHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ".")
}

// RowKey builds the row key for a (host, path) pair: reversed host + path.
func RowKey(host, path string) []byte {
	rh := ReverseHost(host)
	b := make([]byte, 0, len(rh)+len(path))
	b = append(b, rh...)
	b = append(b, path...)
	return b
}

// cellKey encodes a fully-qualified cell key with order-preserving segment
// framing so that (a) row keys keep their lexicographic order — a site's pages
// stay contiguous — and (b) a prefix scan over a row, or a row+family, is one
// engine range. Layout:
//
//	orderedBytes(rowKey) | family(1) | orderedBytes(qualifier) | ^ts(8 BE)
//
// The timestamp is bit-inverted big-endian so the newest version of a cell
// sorts first within its (row, family, qualifier) group.
func cellKey(row []byte, fam Family, qual []byte, tsNanos int64) []byte {
	var b []byte
	b = appendOrdered(b, row)
	b = append(b, byte(fam))
	b = appendOrdered(b, qual)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ^uint64(tsNanos))
	return append(b, ts[:]...)
}

// rowFamilyPrefix is the key prefix shared by every cell of one family in one
// row; Scan(prefix, PrefixEnd(prefix)) walks that family's cells newest-first.
func rowFamilyPrefix(row []byte, fam Family) []byte {
	var b []byte
	b = appendOrdered(b, row)
	return append(b, byte(fam))
}

// appendOrdered writes a length-independent, order-preserving encoding of seg
// to dst: each 0x00 byte becomes 0x00 0xFF and the segment is terminated with
// 0x00 0x00. The terminator sorts below any encoded data byte, so a segment
// that is a prefix of another sorts first — exactly the lexicographic order a
// raw byte compare would give the unframed segments, but now unambiguously
// delimited from the next field.
func appendOrdered(dst, seg []byte) []byte {
	for _, c := range seg {
		if c == 0x00 {
			dst = append(dst, 0x00, 0xFF)
		} else {
			dst = append(dst, c)
		}
	}
	return append(dst, 0x00, 0x00)
}

// Put writes one cell version. tsNanos is the crawl time, which becomes the
// cell's version; writing the same (row, family, qualifier) at different
// timestamps accumulates versions.
func (w *WebTable) Put(row []byte, fam Family, qual []byte, tsNanos int64, value []byte) error {
	return w.eng.Set(cellKey(row, fam, qual, tsNanos), value)
}

// Cell is one decoded version of a column cell.
type Cell struct {
	Qualifier []byte
	TSNanos   int64
	Value     []byte
}

// Latest returns the newest version of a single cell, or ok=false if absent.
// Because versions are stored newest-first, the first key in the cell's range
// is the latest.
func (w *WebTable) Latest(row []byte, fam Family, qual []byte) (Cell, bool, error) {
	prefix := cellKeyQualPrefix(row, fam, qual)
	it := w.eng.Scan(prefix, PrefixEnd(prefix))
	defer func() { _ = it.Close() }()
	if it.Next() {
		ts := decodeTS(it.Key())
		return Cell{Qualifier: clone(qual), TSNanos: ts, Value: clone(it.Value())}, true, it.Err()
	}
	return Cell{}, false, it.Err()
}

// ScanFamily returns the newest version of every cell in a (row, family),
// ordered by qualifier. It collapses the multi-version range to one Cell per
// qualifier, which is what a reader that wants "the current row" needs.
func (w *WebTable) ScanFamily(row []byte, fam Family) ([]Cell, error) {
	prefix := rowFamilyPrefix(row, fam)
	it := w.eng.Scan(prefix, PrefixEnd(prefix))
	defer func() { _ = it.Close() }()
	var out []Cell
	var lastQual []byte
	started := false
	for it.Next() {
		qual := decodeQualifier(it.Key(), row)
		if started && string(qual) == string(lastQual) {
			continue // older version of the same cell; first one seen is newest
		}
		started, lastQual = true, qual
		out = append(out, Cell{Qualifier: qual, TSNanos: decodeTS(it.Key()), Value: clone(it.Value())})
	}
	return out, it.Err()
}

// GCVersions deletes all but the keepN newest versions of every cell in a
// (row, family), the per-family retention the incremental pipeline runs (03.2).
func (w *WebTable) GCVersions(row []byte, fam Family, keepN int) error {
	prefix := rowFamilyPrefix(row, fam)
	it := w.eng.Scan(prefix, PrefixEnd(prefix))
	var lastQual []byte
	started := false
	seen := 0
	var b Batch
	for it.Next() {
		qual := decodeQualifier(it.Key(), row)
		if !started || string(qual) != string(lastQual) {
			started, lastQual = true, clone(qual)
			seen = 1
			continue
		}
		seen++
		if seen > keepN {
			b.Delete(it.Key())
		}
	}
	if err := it.Err(); err != nil {
		_ = it.Close()
		return err
	}
	_ = it.Close()
	if b.Len() == 0 {
		return nil
	}
	return w.eng.Apply(&b)
}

// cellKeyQualPrefix is the prefix shared by every version of one cell.
func cellKeyQualPrefix(row []byte, fam Family, qual []byte) []byte {
	var b []byte
	b = appendOrdered(b, row)
	b = append(b, byte(fam))
	return appendOrdered(b, qual)
}

// decodeTS recovers the crawl timestamp from the trailing 8 bytes of a cell key.
func decodeTS(key []byte) int64 {
	if len(key) < 8 {
		return 0
	}
	return int64(^binary.BigEndian.Uint64(key[len(key)-8:]))
}

// decodeQualifier recovers the qualifier segment from a cell key, given the row
// it belongs to. It skips the ordered-encoded row, the family byte, then decodes
// the ordered-encoded qualifier up to its terminator.
func decodeQualifier(key, row []byte) []byte {
	rowEnc := appendOrdered(nil, row)
	i := len(rowEnc) + 1 // skip row encoding and family byte
	if i > len(key) {
		return nil
	}
	return decodeOrdered(key[i:])
}

// decodeOrdered reverses appendOrdered for the first segment in b, stopping at
// the 0x00 0x00 terminator.
func decodeOrdered(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); i++ {
		if b[i] == 0x00 {
			if i+1 < len(b) && b[i+1] == 0xFF {
				out = append(out, 0x00)
				i++
				continue
			}
			break // terminator
		}
		out = append(out, b[i])
	}
	return out
}
