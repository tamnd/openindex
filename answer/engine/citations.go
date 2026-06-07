package engine

import "openindex/answer/verify"

// parseCitations strips the model's inline citation markers off the answer and
// returns the clean text plus one claim per sentence with the chunk indices it
// cited. The model emits markers like "[1][2]" right after a sentence's
// terminal punctuation (optionally after a space), with 1-based numbers that
// point at the passages it was given; parseCitations turns those into 0-based
// indices and the byte spans of the clean sentences, which is exactly the shape
// verify.Correct and answer/ground consume.
//
// It works on bytes so the spans align with answer/ground, drops the marker
// bytes and the single space that precedes them, and keeps one separating space
// between sentences so the clean text still reads correctly. A sentence with no
// marker becomes a claim with no citation, which verification then reports as
// unsupported.
func parseCitations(raw string) (string, []verify.Claim) {
	b := []byte(raw)
	clean := make([]byte, 0, len(b))
	var claims []verify.Claim
	sentStart := 0
	i := 0
	for i < len(b) {
		c := b[i]
		if !isSentenceEnd(c) {
			clean = append(clean, c)
			i++
			continue
		}
		// Copy the run of terminal punctuation into the clean text.
		for i < len(b) && isSentenceEnd(b[i]) {
			clean = append(clean, b[i])
			i++
		}
		end := len(clean) // the sentence, including its punctuation, ends here

		// Look past a single run of whitespace for citation markers.
		j := i
		for j < len(b) && isSpace(b[j]) {
			j++
		}
		chunks, after, ok := parseMarkers(b, j)

		span := clean[sentStart:end]
		claims = appendClaimSpan(claims, string(span), sentStart, end, chunks)

		if ok {
			// Markers consumed; drop them and the space that preceded them, then
			// put back a single separator if more text follows.
			i = after
			for i < len(b) && isSpace(b[i]) {
				i++
			}
			if i < len(b) {
				clean = append(clean, ' ')
			}
			sentStart = len(clean)
			continue
		}
		sentStart = end
	}
	// A trailing fragment with no terminal punctuation is its own claim.
	if sentStart < len(clean) {
		claims = appendClaimSpan(claims, string(clean[sentStart:]), sentStart, len(clean), nil)
	}
	return string(clean), claims
}

// parseMarkers reads a run of "[n]" markers starting at pos and returns the
// 0-based chunk indices, the offset just past the run, and whether at least one
// marker was found.
func parseMarkers(b []byte, pos int) ([]int, int, bool) {
	var chunks []int
	i := pos
	for i < len(b) && b[i] == '[' {
		k := i + 1
		num := 0
		digits := false
		for k < len(b) && b[k] >= '0' && b[k] <= '9' {
			num = num*10 + int(b[k]-'0')
			k++
			digits = true
		}
		if !digits || k >= len(b) || b[k] != ']' {
			break
		}
		if num > 0 {
			chunks = append(chunks, num-1)
		}
		i = k + 1
	}
	return chunks, i, len(chunks) > 0
}

// appendClaimSpan trims the span text and appends it, skipping an empty span.
func appendClaimSpan(claims []verify.Claim, text string, start, end int, chunks []int) []verify.Claim {
	// Trim leading and trailing space from the visible text but keep the span
	// offsets pointing at the trimmed content.
	for start < end && isSpaceStr(text, 0) {
		text = text[1:]
		start++
	}
	for len(text) > 0 && isSpaceStr(text, len(text)-1) {
		text = text[:len(text)-1]
		end--
	}
	if start >= end || text == "" {
		return claims
	}
	return append(claims, verify.Claim{Text: text, Start: start, End: end, ChunkIndices: chunks})
}

func isSentenceEnd(c byte) bool { return c == '.' || c == '!' || c == '?' }
func isSpace(c byte) bool       { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isSpaceStr(s string, i int) bool {
	return i >= 0 && i < len(s) && isSpace(s[i])
}
