package warc

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
	"time"
)

// fixedClock returns a clock pinned to a known instant so record dates are
// deterministic.
func fixedClock() func() time.Time {
	t := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// inflateMember reads the gzip member at [off, off+length) of buf and returns
// the decompressed record bytes.
func inflateMember(t *testing.T, buf []byte, loc Locator) []byte {
	t.Helper()
	member := buf[loc.Offset : loc.Offset+loc.Length]
	zr, err := gzip.NewReader(bytes.NewReader(member))
	if err != nil {
		t.Fatalf("member at %d is not a valid gzip stream: %v", loc.Offset, err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("inflate member: %v", err)
	}
	return out
}

func TestRecordRoundTrip(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, WithClock(fixedClock()), WithIDStem("test"))

	body := "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n<html>hi</html>"
	loc, err := w.WriteResponse("https://example.com/", []byte(body), map[string]string{
		"WARC-Identified-Payload-Type": "text/html",
	})
	if err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	rec := string(inflateMember(t, out.Bytes(), loc))
	for _, want := range []string{
		"WARC/1.1\r\n",
		"WARC-Type: response\r\n",
		"WARC-Date: 2026-06-07T12:00:00Z\r\n",
		"WARC-Record-ID: <urn:uuid:test-000000000001>\r\n",
		"WARC-Target-URI: https://example.com/\r\n",
		"Content-Type: application/http;msgtype=response\r\n",
		"WARC-Identified-Payload-Type: text/html\r\n",
		body,
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("record missing %q\n--- record ---\n%s", want, rec)
		}
	}

	// The declared Content-Length must equal the block length.
	var clOK bool
	for line := range strings.SplitSeq(rec, "\r\n") {
		if n, ok := parseContentLength(line); ok {
			clOK = true
			if n != len(body) {
				t.Errorf("Content-Length = %d, want %d", n, len(body))
			}
		}
	}
	if !clOK {
		t.Error("no Content-Length header found")
	}
}

// TestMembersAreIndependentlyAddressable is the property the open index relies
// on: each record is its own gzip member, so any one can be inflated from its
// Locator without touching the others.
func TestMembersAreIndependentlyAddressable(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, WithClock(fixedClock()), WithIDStem("test"))

	reqLoc, err := w.WriteRequest("https://example.com/", []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	respLoc, err := w.WriteResponse("https://example.com/", []byte("HTTP/1.1 200 OK\r\n\r\nbody"), nil)
	if err != nil {
		t.Fatal(err)
	}
	metaLoc, err := w.WriteMetadata("https://example.com/", map[string]string{
		"fetchTimeMs": "42",
		"languages":   "en",
	})
	if err != nil {
		t.Fatal(err)
	}

	if w.Records() != 3 {
		t.Fatalf("Records() = %d, want 3", w.Records())
	}
	// Locators must be non-overlapping and in order.
	if reqLoc.Offset != 0 {
		t.Errorf("first record should start at 0, got %d", reqLoc.Offset)
	}
	if respLoc.Offset != reqLoc.Offset+reqLoc.Length {
		t.Errorf("response offset %d should follow request end %d", respLoc.Offset, reqLoc.Offset+reqLoc.Length)
	}
	if metaLoc.Offset != respLoc.Offset+respLoc.Length {
		t.Errorf("metadata offset %d should follow response end %d", metaLoc.Offset, respLoc.Offset+respLoc.Length)
	}

	// Each member inflates on its own and carries the right type.
	if !strings.Contains(string(inflateMember(t, out.Bytes(), reqLoc)), "WARC-Type: request") {
		t.Error("request locator did not inflate to a request record")
	}
	if !strings.Contains(string(inflateMember(t, out.Bytes(), respLoc)), "WARC-Type: response") {
		t.Error("response locator did not inflate to a response record")
	}
	meta := string(inflateMember(t, out.Bytes(), metaLoc))
	if !strings.Contains(meta, "WARC-Type: metadata") {
		t.Error("metadata locator did not inflate to a metadata record")
	}
	// warc-fields block is sorted: fetchTimeMs before languages.
	if i, j := strings.Index(meta, "fetchTimeMs: 42"), strings.Index(meta, "languages: en"); i < 0 || j < 0 || i > j {
		t.Errorf("metadata fields not in sorted order:\n%s", meta)
	}

	// The whole file is still a single valid gzip stream (concatenated members).
	zr, err := gzip.NewReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("concatenated members are not a valid gzip stream: %v", err)
	}
	zr.Multistream(true)
	if _, err := io.ReadAll(zr); err != nil {
		t.Fatalf("reading multistream gzip: %v", err)
	}
}
