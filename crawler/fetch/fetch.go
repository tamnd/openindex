// Package fetch performs the actual HTTP retrieval once the frontier has chosen
// a URL and robots has cleared it (crawler doc 04.4, 04.5). It owns three
// concerns that a naive http.Get gets wrong at web scale:
//
//   - a connection pool tuned for many hosts rather than many requests to one
//     host, so a crawl touching a million domains does not exhaust file
//     descriptors or stall behind head-of-line connections;
//   - forward-confirmed reverse DNS (FCrDNS) verification, so a host claiming to
//     be a search engine — or claiming our own crawler identity — is checked
//     against both directions of DNS before it is trusted (04.5);
//   - a render gate that decides whether a page needs a headless browser, kept
//     behind an interface because rendering is the most expensive thing the
//     crawler does and must be rationed (04.7).
//
// Network resolution and rendering are seams (Resolver, Renderer) so the policy
// is unit-testable without a network or a browser.
package fetch

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

// DefaultTransport returns an http.Transport tuned for breadth-first crawling:
// a large idle-connection pool spread across many hosts, aggressive timeouts so
// a slow host cannot pin a worker, and forced HTTP/2 disabled per-host caps that
// would otherwise serialize a polite single-connection-per-host crawl.
func DefaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// The crawl spreads thin across hosts, so keep many idle conns overall
		// but few per host (politeness already limits per-host concurrency).
		MaxIdleConns:          10000,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		// Bodies are decompressed by the fetcher after a size check, not by the
		// transport, so a malicious gzip bomb cannot inflate unbounded.
		DisableCompression: true,
	}
}

// Resolver looks up the names and addresses the FCrDNS check needs. net.Resolver
// satisfies it; tests inject a static map.
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// VerifyFCrDNS performs forward-confirmed reverse DNS for an IP claiming to
// belong to expectedDomain (04.5). The IP's PTR record must resolve to a name
// under expectedDomain, and that name must forward-resolve back to the same IP.
// Either half failing means the claim is unverified — the pattern used both to
// honor "only fetch what verifies" and to authenticate crawlers that imitate us.
func VerifyFCrDNS(ctx context.Context, r Resolver, ip, expectedDomain string) bool {
	names, err := r.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return false
	}
	expected := strings.ToLower(strings.TrimSuffix(expectedDomain, "."))
	for _, name := range names {
		host := strings.ToLower(strings.TrimSuffix(name, "."))
		if host != expected && !strings.HasSuffix(host, "."+expected) {
			continue
		}
		addrs, err := r.LookupHost(ctx, host)
		if err != nil {
			continue
		}
		if slices.Contains(addrs, ip) {
			return true
		}
	}
	return false
}

// Renderer turns a fetched HTML page into its rendered DOM when JavaScript
// execution is required to see the content (04.7). The production implementation
// drives a headless browser pool; it is an interface so the render decision can
// be tested and so rendering can be rationed or disabled per deployment.
type Renderer interface {
	Render(ctx context.Context, url string, html []byte) ([]byte, error)
}

// NeedsRender decides, cheaply and before paying for a browser, whether a page's
// visible content depends on client-side rendering: a near-empty body that ships
// a script bundle and a mount point is the classic single-page-app signature.
// The render budget is scarce, so the default is not to render (04.7).
func NeedsRender(html []byte) bool {
	s := strings.ToLower(string(html))
	textisch := stripTags(s)
	hasScript := strings.Contains(s, "<script")
	hasAppRoot := strings.Contains(s, `id="root"`) || strings.Contains(s, `id="app"`) ||
		strings.Contains(s, `id="__next"`)
	// Lots of visible text means the server already rendered the content.
	if len(strings.TrimSpace(textisch)) > 512 {
		return false
	}
	return hasScript && hasAppRoot
}

// stripTags removes angle-bracket tags so the heuristic measures visible text,
// not markup. It is intentionally crude; the indexer (doc 05) owns real
// extraction.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
