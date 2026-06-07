// Package segment is the immutable index unit (indexer doc 05.6): a
// self-contained bundle of a term dictionary, posting lists, a forward store,
// and a live-docs bitset. Segments are never mutated in place - a delete clears
// a live bit and space is reclaimed only when the tiered merge policy
// re-indexes live documents into a new segment with fresh dense doc IDs. That
// immutability is what makes the index object-storage-friendly and the open
// publication clean (doc 11).
//
// This is the reference assembly: it wires the real postings codec
// (index/postings), the term-dictionary seam (index/terms), and the forward
// store (index/forward) into one object, holding posting lists in memory rather
// than mmap'd from a single segment file. The on-disk codec layout of doc 05.5
// is the swap target; the access patterns and the merge semantics are what this
// package pins.
package segment

import (
	"sort"

	"openindex"
	"openindex/index/forward"
	"openindex/index/postings"
	"openindex/index/terms"
)

// Segment is a sealed, read-only index segment.
type Segment struct {
	id      openindex.SegmentID
	dict    terms.Dictionary
	lists   []*postings.List // indexed by terms.Entry.PostingOffset
	fwd     *forward.Store
	live    *bitset
	numDocs int
}

// ID returns the segment identifier.
func (s *Segment) ID() openindex.SegmentID { return s.id }

// NumDocs returns the number of documents minted into the segment, including
// any since deleted (the doc-id space). Use LiveDocs for the undeleted count.
func (s *Segment) NumDocs() int { return s.numDocs }

// LiveDocs returns the number of undeleted documents.
func (s *Segment) LiveDocs() int { return s.live.count() }

// Dict exposes the term dictionary for query planning (prefix scans, stats).
func (s *Segment) Dict() terms.Dictionary { return s.dict }

// Document returns the stored fields for a live doc; deleted or unknown ids miss.
func (s *Segment) Document(id openindex.DocID) (forward.Document, bool) {
	if !s.live.get(int(id)) {
		return forward.Document{}, false
	}
	return s.fwd.Get(id)
}

// IsLive reports whether a doc id is undeleted.
func (s *Segment) IsLive(id openindex.DocID) bool { return s.live.get(int(id)) }

// Postings returns a cursor over a term's posting list and the term's document
// frequency. A singleton term (df == 1) is materialized into a one-entry list on
// the fly, so callers see a uniform cursor regardless of the dictionary's
// SingletonDocID optimization (doc 05.3).
func (s *Segment) Postings(term string) (*postings.Cursor, int, bool) {
	e, ok := s.dict.Lookup(term)
	if !ok {
		return nil, 0, false
	}
	if e.Singleton {
		l, _ := postings.Encode([]openindex.Posting{{Doc: e.SingletonDoc, Frequency: e.SingletonFreq}})
		return l.Cursor(), 1, true
	}
	return s.lists[e.PostingOffset].Cursor(), e.DocFreq, true
}

// Delete clears the live bit for id and reports whether it was live. The space
// is reclaimed on the next merge, not now (doc 05.6).
func (s *Segment) Delete(id openindex.DocID) bool {
	if !s.live.get(int(id)) {
		return false
	}
	s.live.clear(int(id))
	return true
}

// Builder accumulates documents and seals a Segment. Documents receive dense,
// sequential doc IDs starting at 0, in the order added.
type Builder struct {
	id    openindex.SegmentID
	docs  []forward.Document
	terms map[string][]openindex.Posting // term -> ascending postings
	next  openindex.DocID
}

// NewBuilder starts a segment build.
func NewBuilder(id openindex.SegmentID) *Builder {
	return &Builder{id: id, terms: make(map[string][]openindex.Posting)}
}

// AddDocument assigns the next doc id, stores doc, and records each term's
// frequency in this document. termFreqs maps a term to its in-document count;
// terms with zero frequency are ignored. It returns the assigned doc id.
func (b *Builder) AddDocument(doc forward.Document, termFreqs map[string]uint32) openindex.DocID {
	id := b.next
	b.next++
	b.docs = append(b.docs, doc)
	for term, f := range termFreqs {
		if f == 0 {
			continue
		}
		b.terms[term] = append(b.terms[term], openindex.Posting{Doc: id, Frequency: f})
	}
	return id
}

// Build seals the accumulated documents into an immutable Segment: it encodes
// each term's posting list (turning df==1 terms into SingletonDocID dictionary
// entries), builds the term dictionary, writes the forward store, and marks
// every doc live.
func (b *Builder) Build() (*Segment, error) {
	// Posting lists are appended in doc-id order, so each term's slice is already
	// ascending; build the dictionary and the posting-list table together.
	termList := make([]terms.Term, 0, len(b.terms))
	lists := make([]*postings.List, 0, len(b.terms))
	for term, ps := range b.terms {
		e := terms.Entry{DocFreq: len(ps)}
		if len(ps) == 1 {
			e.Singleton = true
			e.SingletonDoc = ps[0].Doc
			e.SingletonFreq = ps[0].Frequency
		} else {
			l, err := postings.Encode(ps)
			if err != nil {
				return nil, err
			}
			e.PostingOffset = int64(len(lists))
			lists = append(lists, l)
		}
		termList = append(termList, terms.Term{Term: term, Entry: e})
	}

	fw := forward.NewWriter(forward.Identity{})
	for i, doc := range b.docs {
		if err := fw.Add(openindex.DocID(i), doc); err != nil {
			return nil, err
		}
	}

	live := newBitset(len(b.docs))
	live.setAll()

	return &Segment{
		id:      b.id,
		dict:    terms.NewSortedDict(termList),
		lists:   lists,
		fwd:     fw.Seal(),
		live:    live,
		numDocs: len(b.docs),
	}, nil
}

// Merge re-indexes the live documents of several segments into one new segment
// with fresh dense doc IDs, in the input order, dropping deleted documents - the
// space-reclaiming half of the tiered merge (doc 05.6). The terms of all inputs
// are unioned and their posting lists concatenated in new-id order, which stays
// sorted because new ids are assigned in a single ascending pass.
func Merge(newID openindex.SegmentID, segs ...*Segment) (*Segment, error) {
	b := NewBuilder(newID)
	for _, s := range segs {
		// Re-derive each live document's term frequencies from its posting lists.
		// Walk every term once; the per-doc frequency map is assembled as we go.
		perDoc := make(map[openindex.DocID]map[string]uint32)
		collectTerms(s, perDoc)
		for old := range openindex.DocID(s.numDocs) {
			if !s.live.get(int(old)) {
				continue
			}
			doc, ok := s.fwd.Get(old)
			if !ok {
				continue
			}
			b.AddDocument(doc, perDoc[old])
		}
	}
	return b.Build()
}

// collectTerms inverts a segment's postings into a per-document term-frequency
// map for the live documents, the input Merge's builder needs.
func collectTerms(s *Segment, out map[openindex.DocID]map[string]uint32) {
	visit := func(term string, c *postings.Cursor) {
		for c.Next() {
			d := c.Doc()
			if !s.live.get(int(d)) {
				continue
			}
			m := out[d]
			if m == nil {
				m = make(map[string]uint32)
				out[d] = m
			}
			m[term] = c.Freq()
		}
	}
	// Enumerate all terms via a full prefix scan on the empty prefix.
	var allTerms []string
	s.dict.PrefixScan("", func(term string, _ terms.Entry) bool {
		allTerms = append(allTerms, term)
		return true
	})
	sort.Strings(allTerms)
	for _, term := range allTerms {
		c, _, ok := s.Postings(term)
		if ok {
			visit(term, c)
		}
	}
}
