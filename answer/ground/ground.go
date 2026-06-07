// Package ground is the span-level grounding and citation data model
// (architecture doc 09.2), copied from Google's Gemini grounding metadata
// because that shape is the one that survives verification: a coarse,
// passage-level citation cannot be checked, so each claim ties to a specific
// span of the answer and a specific set of sources.
//
// Two details here are easy to get wrong and are fixed once, in this package,
// so nothing downstream has to get them right again:
//
//   - Offsets are byte offsets into the UTF-8 answer, not rune or character
//     offsets. For multi-byte text this is the difference between a citation
//     marker that lands on a character boundary and one that splits a rune and
//     corrupts the output. Insert operates on []byte throughout.
//   - Markers are inserted in reverse order by end offset, so inserting one
//     marker never shifts the offsets of an earlier span that has not been
//     processed yet.
package ground

import (
	"fmt"
	"sort"
	"strings"
)

// Chunk is one source the answer draws on: a uri and a human title. It is the
// groundingChunks entry of the Gemini model. The index of a chunk in the
// answer's chunk slice is what a Support points at.
type Chunk struct {
	URI   string
	Title string
}

// Segment is a span of the answer text, addressed by byte offset. Start is
// inclusive and End is exclusive, the usual half-open convention, so the span
// is Text[Start:End] and its length is End - Start. Text is the span content,
// carried so a support can be validated against the answer without re-slicing.
type Segment struct {
	Start int
	End   int
	Text  string
}

// Support is the grounding for one claim: the span of the answer it covers and
// the chunk indices that back it. It is the groundingSupports entry. A claim
// with no backing chunks is a claim with no evidence, which verification
// (answer/verify) is responsible for catching before a Support is ever built
// for it.
type Support struct {
	Segment ChunkSegment
	Chunks  []int
}

// ChunkSegment is Segment under the name the wire model uses, kept as a
// distinct type so the public field reads as segment.Start rather than an
// embedded anonymous field.
type ChunkSegment = Segment

// Metadata is the full grounding record for an answer: the sources and the
// per-claim supports over them. It is what ships to the client alongside the
// answer text, and it is what a reader follows back to the archived records.
type Metadata struct {
	Chunks   []Chunk
	Supports []Support
}

// Validate checks that the metadata is internally consistent against the answer
// text: every support span is in range and ordered, its recorded text matches
// the bytes at its offsets, and every chunk index it cites exists. It returns
// the first problem it finds, so a malformed grounding record is rejected
// before it reaches a reader rather than producing a corrupt citation marker.
func (m Metadata) Validate(answer string) error {
	b := []byte(answer)
	for i, s := range m.Supports {
		seg := s.Segment
		if seg.Start < 0 || seg.End > len(b) || seg.Start > seg.End {
			return fmt.Errorf("support %d: span [%d,%d) out of range for %d bytes", i, seg.Start, seg.End, len(b))
		}
		if got := string(b[seg.Start:seg.End]); got != seg.Text {
			return fmt.Errorf("support %d: span text %q does not match bytes %q", i, seg.Text, got)
		}
		if len(s.Chunks) == 0 {
			return fmt.Errorf("support %d: span [%d,%d) has no backing chunk", i, seg.Start, seg.End)
		}
		for _, c := range s.Chunks {
			if c < 0 || c >= len(m.Chunks) {
				return fmt.Errorf("support %d: chunk index %d out of range for %d chunks", i, c, len(m.Chunks))
			}
		}
	}
	return nil
}

// Marker renders the citation marker for a support: the 1-based chunk numbers
// in brackets, for example "[1][3]". It is the visible form the reader sees and
// can click. The numbers are 1-based because that is how a citation reads on a
// page, while the stored Chunks are 0-based indices.
func Marker(chunks []int) string {
	var sb strings.Builder
	for _, c := range chunks {
		fmt.Fprintf(&sb, "[%d]", c+1)
	}
	return sb.String()
}

// Insert places a citation marker at the end of each supported span and returns
// the annotated answer. It is the operation the two correctness details above
// exist for: it copies the supports, sorts them by descending end offset, and
// inserts each marker at its span end, so an earlier marker never moves a later
// span's offsets and the byte offsets stay valid as the string grows.
//
// Insert assumes the metadata has already passed Validate against the same
// answer; it does not re-check ranges, because a caller that skips validation
// has a bug to fix rather than an error to handle.
func Insert(answer string, supports []Support) string {
	if len(supports) == 0 {
		return answer
	}
	ordered := make([]Support, len(supports))
	copy(ordered, supports)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Segment.End > ordered[j].Segment.End
	})

	b := []byte(answer)
	for _, s := range ordered {
		marker := []byte(Marker(s.Chunks))
		at := s.Segment.End
		out := make([]byte, 0, len(b)+len(marker))
		out = append(out, b[:at]...)
		out = append(out, marker...)
		out = append(out, b[at:]...)
		b = out
	}
	return string(b)
}
