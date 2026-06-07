package ground

import "testing"

func seg(start, end int, text string) Segment { return Segment{Start: start, End: end, Text: text} }

func TestInsertSingleMarker(t *testing.T) {
	answer := "The sky is blue."
	// "The sky is blue" is bytes [0,15); the period follows.
	supports := []Support{{Segment: seg(0, 15, "The sky is blue"), Chunks: []int{0}}}
	got := Insert(answer, supports)
	want := "The sky is blue[1]."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInsertReverseOrderKeepsOffsetsValid(t *testing.T) {
	// Two spans in the same answer. Inserting the first marker would shift the
	// second span's offsets if done left to right; reverse order keeps both
	// markers landing where they belong.
	answer := "Cats purr. Dogs bark."
	supports := []Support{
		{Segment: seg(0, 10, "Cats purr."), Chunks: []int{0}},
		{Segment: seg(11, 21, "Dogs bark."), Chunks: []int{1, 2}},
	}
	got := Insert(answer, supports)
	want := "Cats purr.[1] Dogs bark.[2][3]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInsertIsOrderIndependent(t *testing.T) {
	// The same supports given in either order must produce the same annotated
	// answer, because Insert sorts internally.
	answer := "One. Two. Three."
	a := []Support{
		{Segment: seg(0, 4, "One."), Chunks: []int{0}},
		{Segment: seg(5, 9, "Two."), Chunks: []int{1}},
		{Segment: seg(10, 16, "Three."), Chunks: []int{2}},
	}
	b := []Support{a[2], a[0], a[1]}
	if Insert(answer, a) != Insert(answer, b) {
		t.Fatalf("insert depended on support order: %q vs %q", Insert(answer, a), Insert(answer, b))
	}
}

func TestInsertByteOffsetsSurviveMultibyte(t *testing.T) {
	// The answer has multi-byte runes before the cited span. A character-offset
	// implementation would land the marker inside a rune; byte offsets place it
	// on the boundary. The span is the word "fjords".
	answer := "Naïve café fjords."
	start := len([]byte("Naïve café "))
	end := start + len("fjords")
	supports := []Support{{Segment: seg(start, end, "fjords"), Chunks: []int{0}}}
	got := Insert(answer, supports)
	want := "Naïve café fjords[1]."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInsertNoSupportsIsIdentity(t *testing.T) {
	answer := "Nothing to cite."
	if got := Insert(answer, nil); got != answer {
		t.Fatalf("got %q, want unchanged %q", got, answer)
	}
}

func TestMarkerIsOneBased(t *testing.T) {
	if got := Marker([]int{0, 2}); got != "[1][3]" {
		t.Fatalf("got %q, want [1][3]", got)
	}
}

func TestValidateAccepts(t *testing.T) {
	answer := "Cats purr."
	m := Metadata{
		Chunks:   []Chunk{{URI: "https://example.test/cats", Title: "Cats"}},
		Supports: []Support{{Segment: seg(0, 10, "Cats purr."), Chunks: []int{0}}},
	}
	if err := m.Validate(answer); err != nil {
		t.Fatalf("valid metadata rejected: %v", err)
	}
}

func TestValidateRejectsOutOfRangeSpan(t *testing.T) {
	answer := "short"
	m := Metadata{
		Chunks:   []Chunk{{URI: "u"}},
		Supports: []Support{{Segment: seg(0, 99, "short"), Chunks: []int{0}}},
	}
	if err := m.Validate(answer); err == nil {
		t.Fatal("a span past the end of the answer should be rejected")
	}
}

func TestValidateRejectsMismatchedSpanText(t *testing.T) {
	answer := "Cats purr."
	m := Metadata{
		Chunks:   []Chunk{{URI: "u"}},
		Supports: []Support{{Segment: seg(0, 10, "Dogs bark."), Chunks: []int{0}}},
	}
	if err := m.Validate(answer); err == nil {
		t.Fatal("a span whose recorded text does not match the bytes should be rejected")
	}
}

func TestValidateRejectsMissingChunk(t *testing.T) {
	answer := "Cats purr."
	m := Metadata{
		Chunks:   []Chunk{{URI: "u"}},
		Supports: []Support{{Segment: seg(0, 10, "Cats purr."), Chunks: []int{5}}},
	}
	if err := m.Validate(answer); err == nil {
		t.Fatal("a citation of a nonexistent chunk should be rejected")
	}
}

func TestValidateRejectsUnsupportedSpan(t *testing.T) {
	answer := "Cats purr."
	m := Metadata{
		Chunks:   []Chunk{{URI: "u"}},
		Supports: []Support{{Segment: seg(0, 10, "Cats purr."), Chunks: nil}},
	}
	if err := m.Validate(answer); err == nil {
		t.Fatal("a span with no backing chunk should be rejected")
	}
}
