package robots

import (
	"strings"
	"testing"
	"time"
)

func parse(s string) *Robots { return Parse(strings.NewReader(s)) }

func TestDisallowAllowBasic(t *testing.T) {
	rb := parse("User-agent: *\nDisallow: /private\nAllow: /public\n")
	cases := map[string]bool{
		"/public/x":  true,
		"/private/x": false,
		"/other":     true,
	}
	for path, want := range cases {
		if got := rb.Allowed("OpenIndexBot", path); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestLongestMatchWins is the rule order-independent arbitration of RFC 9309.
func TestLongestMatchWins(t *testing.T) {
	// Disallow is longer for /folder/page, so it wins even though Allow is listed
	// after and matches the shorter /folder.
	rb := parse("User-agent: *\nAllow: /folder\nDisallow: /folder/page\n")
	if rb.Allowed("bot", "/folder/page") {
		t.Error("/folder/page should be disallowed (longer disallow wins)")
	}
	if !rb.Allowed("bot", "/folder/other") {
		t.Error("/folder/other should be allowed")
	}
}

// TestAllowWinsOnTie: equal-length allow and disallow both match -> allow wins.
func TestAllowWinsOnTie(t *testing.T) {
	rb := parse("User-agent: *\nDisallow: /page\nAllow: /page\n")
	if !rb.Allowed("bot", "/page") {
		t.Error("equal-length tie must resolve to allow")
	}
}

func TestWildcardStar(t *testing.T) {
	rb := parse("User-agent: *\nDisallow: /*.pdf\n")
	if rb.Allowed("bot", "/docs/report.pdf") {
		t.Error("/docs/report.pdf should match /*.pdf")
	}
	if !rb.Allowed("bot", "/docs/report.html") {
		t.Error("/docs/report.html should not match /*.pdf")
	}
}

func TestWildcardEndAnchor(t *testing.T) {
	rb := parse("User-agent: *\nDisallow: /*.php$\n")
	if rb.Allowed("bot", "/index.php") {
		t.Error("/index.php should match /*.php$")
	}
	// Anchored: .php must be the end, so a trailing query means no match.
	if !rb.Allowed("bot", "/index.php?x=1") {
		t.Error("/index.php?x=1 should not match the end-anchored /*.php$")
	}
}

func TestPathCaseSensitive(t *testing.T) {
	rb := parse("User-agent: *\nDisallow: /Page\n")
	if rb.Allowed("bot", "/Page") {
		t.Error("/Page should be disallowed")
	}
	if !rb.Allowed("bot", "/page") {
		t.Error("/page must not match /Page (path matching is case-sensitive)")
	}
}

func TestUserAgentCaseInsensitiveAndSpecificity(t *testing.T) {
	doc := "User-agent: *\nDisallow: /\n\nUser-agent: OpenIndexBot\nDisallow: /private\n"
	rb := parse(doc)
	// The specific bot's group applies (case-insensitively), so / is allowed but
	// /private is not.
	if !rb.Allowed("openindexbot/1.0", "/home") {
		t.Error("specific group should allow /home")
	}
	if rb.Allowed("OPENINDEXBOT", "/private/x") {
		t.Error("specific group should disallow /private")
	}
	// An unknown bot falls back to the * group, which disallows everything.
	if rb.Allowed("RandomCrawler", "/home") {
		t.Error("unknown bot should fall back to * group (disallow /)")
	}
}

func TestEmptyDisallowAllowsAll(t *testing.T) {
	rb := parse("User-agent: *\nDisallow:\n")
	if !rb.Allowed("bot", "/anything") {
		t.Error("empty Disallow means allow all")
	}
}

func TestConsecutiveUserAgentsShareGroup(t *testing.T) {
	rb := parse("User-agent: a\nUser-agent: b\nDisallow: /x\n")
	if rb.Allowed("a-bot containing a", "/x") {
		t.Error("agent a should share the group")
	}
	if rb.Allowed("b", "/x") {
		t.Error("agent b should share the group")
	}
}

func TestCrawlDelay(t *testing.T) {
	rb := parse("User-agent: *\nCrawl-delay: 2.5\n")
	d, ok := rb.CrawlDelay("bot")
	if !ok || d != 2500*time.Millisecond {
		t.Errorf("CrawlDelay = %v,%v want 2.5s,true", d, ok)
	}
	rb2 := parse("User-agent: *\nDisallow: /\n")
	if _, ok := rb2.CrawlDelay("bot"); ok {
		t.Error("no crawl-delay declared")
	}
}

func TestSitemapsAndComments(t *testing.T) {
	rb := parse("# a comment\nSitemap: https://example.com/sitemap.xml\nUser-agent: * # inline\nDisallow: /x # trailing\n")
	if len(rb.Sitemaps()) != 1 || rb.Sitemaps()[0] != "https://example.com/sitemap.xml" {
		t.Errorf("sitemaps = %v", rb.Sitemaps())
	}
	if rb.Allowed("bot", "/x") {
		t.Error("/x disallowed despite inline comment on the line")
	}
}

func TestNoGroupAllowsAll(t *testing.T) {
	rb := parse("")
	if !rb.Allowed("bot", "/anything") {
		t.Error("empty robots.txt allows everything")
	}
}

func TestParseBoundIsRespected(t *testing.T) {
	// A rule pushed past MaxBytes by padding must be ignored.
	pad := strings.Repeat("# pad\n", (MaxBytes/6)+10)
	rb := parse("User-agent: *\n" + pad + "Disallow: /late\n")
	if !rb.Allowed("bot", "/late") {
		t.Error("rule beyond MaxBytes must be ignored")
	}
}
