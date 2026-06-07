// MemSource is the in-process reference Source. It holds a segment's postings
// lists and document records in memory and yields them in CIFF order. It is what
// the exporter tests and a small export run against; the production Source reads
// the same shape straight off the segment FST and postings store (doc 05) without
// materializing the lists.

package ciff

import (
	"iter"
	"slices"
)

// MemSource is a Source backed by in-memory slices. Build one with NewMemSource,
// which sorts the inputs into CIFF order and computes the header statistics, so a
// caller hands over unordered data and gets a valid source back.
type MemSource struct {
	header Header
	terms  []PostingsList
	docs   []DocRecord
}

// NewMemSource returns a Source over the given postings lists and document
// records. It sorts the terms lexicographically and each list's postings by
// ascending document id (the order CIFF requires and the exporter assumes), and
// derives the header: the list and document counts, the collection token count
// (the sum of document lengths), and the average document length.
func NewMemSource(desc string, terms []PostingsList, docs []DocRecord) *MemSource {
	terms = slices.Clone(terms)
	slices.SortFunc(terms, func(a, b PostingsList) int {
		switch {
		case a.Term < b.Term:
			return -1
		case a.Term > b.Term:
			return 1
		default:
			return 0
		}
	})
	for i := range terms {
		terms[i].Postings = slices.Clone(terms[i].Postings)
		slices.SortFunc(terms[i].Postings, func(a, b Posting) int {
			return int(a.DocID) - int(b.DocID)
		})
	}
	docs = slices.Clone(docs)
	slices.SortFunc(docs, func(a, b DocRecord) int {
		return int(a.DocID) - int(b.DocID)
	})

	var totalTerms int64
	for _, d := range docs {
		totalTerms += int64(d.Length)
	}
	avg := 0.0
	if len(docs) > 0 {
		avg = float64(totalTerms) / float64(len(docs))
	}
	return &MemSource{
		header: Header{
			NumPostingsLists: len(terms),
			NumDocs:          len(docs),
			TotalTerms:       totalTerms,
			AverageDocLength: avg,
			Description:      desc,
		},
		terms: terms,
		docs:  docs,
	}
}

// Header returns the collection statistics.
func (m *MemSource) Header() Header { return m.header }

// Terms yields the postings lists in ascending term order.
func (m *MemSource) Terms() iter.Seq[PostingsList] { return slices.Values(m.terms) }

// Docs yields the document records in ascending internal-id order.
func (m *MemSource) Docs() iter.Seq[DocRecord] { return slices.Values(m.docs) }
