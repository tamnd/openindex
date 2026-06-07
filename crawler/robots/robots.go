// Package robots implements the Robots Exclusion Protocol as standardized in
// RFC 9309, the access-control half of the crawler's provenance contract
// (crawler doc 04.3, architecture doc 03). It is deliberately strict about the
// rules a casual parser gets wrong — longest-match-wins independent of order,
// case-insensitive user-agent matching but case-sensitive path matching,
// `*`/`$` wildcards, the 500 KiB parse bound — because mis-parsing robots.txt
// is a consent violation, not a bug.
//
// crawl-delay is parsed and exposed as a best-effort politeness hint (it is not
// part of RFC 9309); the frontier feeds it into the per-host back-off and never
// presents it as a standards guarantee.
package robots

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"
)

// MaxBytes is the maximum amount of a robots.txt the parser reads; content past
// it is ignored, matching Google's enforced bound (04.3).
const MaxBytes = 500 << 10 // 500 KiB

// rule is one allow/disallow directive with its match pattern.
type rule struct {
	allow   bool
	pattern string
}

// group is the set of rules and the crawl-delay for one or more user-agent
// product tokens.
type group struct {
	agents     []string // lowercased product tokens this group applies to
	rules      []rule
	crawlDelay time.Duration
	hasDelay   bool
}

// Robots is a parsed robots.txt ready to answer access questions for a given
// user agent. It is immutable after Parse and safe for concurrent use.
type Robots struct {
	groups   []group
	sitemaps []string
}

// Parse reads a robots.txt body (up to MaxBytes) into a Robots. It never errors
// on malformed input: per RFC 9309 an unparseable line is skipped, because a
// crawler must degrade toward the site's intent, not toward a parse failure.
func Parse(r io.Reader) *Robots {
	rb := &Robots{}
	sc := bufio.NewScanner(io.LimitReader(r, MaxBytes))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	var cur *group
	// startedRules tracks whether we have seen a rule since the last user-agent
	// line; consecutive user-agent lines accumulate into one group.
	startedRules := false

	for sc.Scan() {
		line := stripComment(sc.Text())
		field, value, ok := splitField(line)
		if !ok {
			continue
		}
		switch field {
		case "user-agent":
			if cur == nil || startedRules {
				rb.groups = append(rb.groups, group{})
				cur = &rb.groups[len(rb.groups)-1]
				startedRules = false
			}
			if v := strings.ToLower(strings.TrimSpace(value)); v != "" {
				cur.agents = append(cur.agents, v)
			}
		case "allow", "disallow":
			if cur == nil {
				continue // a rule before any user-agent line has no group
			}
			startedRules = true
			// An empty Disallow means "allow all" and is represented by simply
			// having no blocking rule; skip it rather than store an empty pattern.
			if field == "disallow" && strings.TrimSpace(value) == "" {
				continue
			}
			cur.rules = append(cur.rules, rule{allow: field == "allow", pattern: value})
		case "crawl-delay":
			if cur == nil {
				continue
			}
			startedRules = true
			if d, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && d >= 0 {
				cur.crawlDelay = time.Duration(d * float64(time.Second))
				cur.hasDelay = true
			}
		case "sitemap":
			if s := strings.TrimSpace(value); s != "" {
				rb.sitemaps = append(rb.sitemaps, s)
			}
		}
	}
	return rb
}

// Sitemaps returns the sitemap URLs declared in the file; the frontier seeds
// from them (04.3).
func (rb *Robots) Sitemaps() []string { return rb.sitemaps }

// groupFor selects the group whose product token best matches userAgent. RFC
// 9309: matching is case-insensitive on a substring of the UA, the most
// specific (longest) matching token wins, and a "*" group is the fallback used
// only when no specific token matches.
func (rb *Robots) groupFor(userAgent string) *group {
	ua := strings.ToLower(userAgent)
	var best *group
	bestLen := -1
	var star *group
	for i := range rb.groups {
		g := &rb.groups[i]
		for _, a := range g.agents {
			if a == "*" {
				if star == nil {
					star = g
				}
				continue
			}
			if strings.Contains(ua, a) && len(a) > bestLen {
				best, bestLen = g, len(a)
			}
		}
	}
	if best != nil {
		return best
	}
	return star
}

// Allowed reports whether userAgent may fetch the given path. Per RFC 9309 the
// rule with the longest matching pattern wins regardless of file order, and a
// tie between an allow and a disallow is resolved in favor of allow. With no
// matching rule (or no applicable group) access is allowed.
func (rb *Robots) Allowed(userAgent, path string) bool {
	g := rb.groupFor(userAgent)
	if g == nil {
		return true
	}
	path = normalizePath(path)
	bestLen := -1
	allow := true
	for _, r := range g.rules {
		n, ok := matchLen(r.pattern, path)
		if !ok {
			continue
		}
		if n > bestLen || (n == bestLen && r.allow) {
			bestLen = n
			allow = r.allow
		}
	}
	return allow
}

// CrawlDelay returns the crawl-delay hint for userAgent and whether one was
// declared. It is advisory (04.3).
func (rb *Robots) CrawlDelay(userAgent string) (time.Duration, bool) {
	g := rb.groupFor(userAgent)
	if g == nil || !g.hasDelay {
		return 0, false
	}
	return g.crawlDelay, true
}

// matchLen reports whether pattern matches path and, if so, the number of
// pattern octets that matched (used for longest-match arbitration). The pattern
// language is RFC 9309: `*` matches any run of characters, `$` anchors the end
// of the path, everything else is literal and case-sensitive.
func matchLen(pattern, path string) (int, bool) {
	anchored := strings.HasSuffix(pattern, "$")
	pat := pattern
	if anchored {
		pat = pat[:len(pat)-1]
	}
	// The match length is the count of literal+wildcard pattern characters, used
	// only to compare specificity, so it is just len(pat).
	if matchGlob(pat, path, anchored) {
		return len(pat), true
	}
	return 0, false
}

// matchGlob reports whether pattern matches a prefix of s, with `*` matching any
// run of characters. A robots pattern is a prefix match by default — "/private"
// matches "/private/x" — so success is declared the moment the pattern is
// exhausted; when anchored ($) the match must additionally reach the end of s.
// It is a two-pointer glob with backtracking on the last `*`.
func matchGlob(pattern, s string, anchored bool) bool {
	starP, starS := -1, 0
	p, j := 0, 0
	for {
		if p == len(pattern) {
			if !anchored || j == len(s) {
				return true
			}
			// Anchored with input left: let the last `*` swallow one more byte.
		} else if pattern[p] == '*' {
			starP, starS = p, j
			p++
			continue
		} else if j < len(s) && pattern[p] == s[j] {
			p++
			j++
			continue
		}
		// Mismatch (or anchored-with-leftover): backtrack to the last `*` and
		// extend the run it matches by one byte.
		if starP < 0 || starS >= len(s) {
			return false
		}
		starS++
		j = starS
		p = starP + 1
	}
}

// normalizePath ensures a path used for matching starts with "/" and is at
// least "/". Query and fragment are kept because robots patterns may match them.
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// stripComment removes a `#` comment from a line.
func stripComment(line string) string {
	if before, _, found := strings.Cut(line, "#"); found {
		return before
	}
	return line
}

// splitField splits "field: value" into a lowercased field and its raw value.
func splitField(line string) (field, value string, ok bool) {
	name, val, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	field = strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(val)
	if field == "" {
		return "", "", false
	}
	return field, value, true
}
