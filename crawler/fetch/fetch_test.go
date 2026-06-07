package fetch

import (
	"context"
	"strings"
	"testing"
)

// staticResolver answers from in-memory maps so FCrDNS logic is tested without
// touching the network.
type staticResolver struct {
	ptr  map[string][]string // ip -> names
	host map[string][]string // name -> ips
}

func (s staticResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	return s.ptr[addr], nil
}
func (s staticResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	return s.host[host], nil
}

func TestVerifyFCrDNS(t *testing.T) {
	r := staticResolver{
		ptr: map[string][]string{
			"66.249.66.1": {"crawl-66-249-66-1.googlebot.com."},
			"10.0.0.9":    {"evil.example.net."},
			"10.0.0.7":    {"spoof.googlebot.com."},
		},
		host: map[string][]string{
			"crawl-66-249-66-1.googlebot.com": {"66.249.66.1"},
			"spoof.googlebot.com":             {"10.0.0.99"}, // forward does not confirm
		},
	}
	ctx := context.Background()

	if !VerifyFCrDNS(ctx, r, "66.249.66.1", "googlebot.com") {
		t.Error("legitimate googlebot IP should verify")
	}
	if VerifyFCrDNS(ctx, r, "10.0.0.9", "googlebot.com") {
		t.Error("PTR outside the claimed domain must not verify")
	}
	if VerifyFCrDNS(ctx, r, "10.0.0.7", "googlebot.com") {
		t.Error("PTR in domain but non-confirming forward lookup must not verify")
	}
	if VerifyFCrDNS(ctx, r, "1.2.3.4", "googlebot.com") {
		t.Error("an IP with no PTR record must not verify")
	}
}

func TestNeedsRender(t *testing.T) {
	spa := []byte(`<html><head><script src="/bundle.js"></script></head><body><div id="root"></div></body></html>`)
	if !NeedsRender(spa) {
		t.Error("an empty-body SPA shell should be flagged for rendering")
	}

	serverRendered := []byte("<html><body><article>" +
		strings.Repeat("real visible article text that the server already rendered ", 20) +
		"</article><script src=/analytics.js></script></body></html>")
	if NeedsRender(serverRendered) {
		t.Error("a content-rich server-rendered page should not need rendering")
	}

	plain := []byte("<html><body><p>short but no script and no app root</p></body></html>")
	if NeedsRender(plain) {
		t.Error("a page with no script/app-root signature should not need rendering")
	}
}

func TestDefaultTransportTuning(t *testing.T) {
	tr := DefaultTransport()
	if tr.MaxIdleConnsPerHost > tr.MaxIdleConns {
		t.Error("per-host idle cap should be far below the global idle pool")
	}
	if !tr.DisableCompression {
		t.Error("transport should not auto-decompress; the fetcher size-checks first")
	}
	if tr.ResponseHeaderTimeout == 0 {
		t.Error("a response-header timeout is required so a slow host cannot pin a worker")
	}
}
