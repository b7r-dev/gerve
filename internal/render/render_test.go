package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/b7r-dev/gerve/internal/git"
)

func fixtureBase() Base {
	return Base{
		Version:  "test",
		RepoName: "dotgrid",
		Ref:      "HEAD",
		Head:     git.HeadInfo{Branch: "main"},
		Refs:     []git.RefInfo{{Name: "main", Kind: "branch", Hash: "abc123def4567890123456789012345678901234"}},
		Route:    "tree",
		Path:     "",
		Crumbs:   []Crumb{{Name: "src", URL: "/tree/HEAD/src"}},
	}
}

func commit() git.CommitInfo {
	return git.CommitInfo{Hash: "abc123def4567890123456789012345678901234", Short: "abc123d", Author: "Maya", Time: time.Now().Add(-time.Hour), Subject: "fix: the thing"}
}

// TestPagesRenderNonEmpty executes every page with representative data and
// fails on the classic blank-page failure mode (define-only parse clobbering
// the layout body) or on missing template references.
func TestPagesRenderNonEmpty(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := fixtureBase()
	c := commit()

	pages := map[string]any{
		"tree": TreePage{Base: b, Entries: []git.Entry{{Name: "main.go", Path: "main.go", InGit: true, Last: &c}}, DirLast: &c, Overlay: true,
			Readme: "<p>readme</p>", ReadmeName: "README.md", Recent: []git.CommitInfo{c}},
		"blob":    BlobPage{Base: b, Blob: &git.Blob{Path: "main.go", Size: 10, Content: []byte("package main")}, Last: &c, Lines: 1, Code: "<div>code</div>", CanBlame: true, CanHistory: true},
		"blame":   BlamePage{Base: b, Groups: []git.BlameGroup{{Commit: c, Lines: []git.BlameLine{{Num: 1, Text: "package main"}}}}},
		"commits": CommitsPage{Base: b, Log: &git.LogPage{Commits: []git.CommitInfo{c}, Next: true}, Page: 0, NextURL: "/commits/HEAD?page=1"},
		"commit": CommitPage{Base: b, Meta: &git.CommitMeta{CommitInfo: c, Body: "body", Parents: []string{"def4567901234567890123456789012345678901"}}, Merge: true,
			Files: []FileView{{FileDiff: git.FileDiff{Status: "R", Old: "a.txt", New: "b.txt", Adds: 1, Dels: 1, Patch: "@@ -1,1 +1,1 @@\n-a\n+b\n"}, Rows: git.UnifiedRows("@@ -1,1 +1,1 @@\n-a\n+b\n")}}, View: "unified"},
		"refs":  RefsPage{Base: b, Branches: []RefsEntry{{RefInfo: git.RefInfo{Name: "main", Kind: "branch"}, Info: c}}},
		"error": ErrorPage{Base: b, Code: 404, Title: "not found", Message: "nope"},
		"empty": EmptyPage{Base: b},
	}

	for name, data := range pages {
		var out bytes.Buffer
		rd.pages[name].ExecuteTemplate(&out, "layout", data)
		got := out.String()
		if !strings.Contains(got, "<!doctype html>") {
			t.Errorf("page %q: layout body did not render (blank-page failure)", name)
			continue
		}
		if !strings.Contains(got, "gerve test") {
			t.Errorf("page %q: footer missing", name)
		}
	}
}

// PlainCode carries the same anchors as Highlight: a line of plain text must be
// as addressable as a line of syntax-highlighted code, blank lines included — a
// link to a blank line is still a link to a line.
func TestPlainCodeRowsAreAddressable(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	out := string(rd.PlainCode([]byte("first\n\nthird\n")))
	for _, want := range []string{`<tr id="L1">`, `<tr id="L2">`, `<tr id="L3">`} {
		if !strings.Contains(out, want) {
			t.Errorf("plain code missing %s:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "<tr id="); n != 3 {
		t.Errorf("rows = %d, want 3 (the blank line is a line too)", n)
	}

	// A file ending in a blank line ends in an addressable row as well: the
	// anchor has to exist for the line that has no text.
	out = string(rd.PlainCode([]byte("a\nb\n\n")))
	if n := strings.Count(out, "<tr id="); n != 3 {
		t.Errorf("trailing blank line: rows = %d, want 3\n%s", n, out)
	}
	if !strings.Contains(out, `<tr id="L3"><td class="ln">3</td><td class="lt"></td></tr>`) {
		t.Errorf("trailing blank line is not addressable:\n%s", out)
	}
}

// TestEmphasiseMatchEscapesBothHalves: the text comes out of a file and the
// query out of a text box, so both are input a stranger can influence — and the
// markup around the match is built by hand, which is exactly where escaping must
// not be skipped.
func TestEmphasiseMatchEscapesBothHalves(t *testing.T) {
	got := string(emphasiseMatch(`a <img src=x onerror=alert(1)> nee&dle`, "nee&dle"))
	if strings.Contains(got, "<img") {
		t.Errorf("matched text was not escaped: %s", got)
	}
	if !strings.Contains(got, `<em class="hit">nee&amp;dle</em>`) {
		t.Errorf("the match is not emphasised, or not escaped: %s", got)
	}

	got = string(emphasiseMatch(`x <script>alert(1)</script>`, "<script>"))
	if strings.Contains(got, "<script>") {
		t.Errorf("a markup-shaped query was not escaped: %s", got)
	}
	if !strings.Contains(got, `<em class="hit">&lt;script&gt;</em>`) {
		t.Errorf("escaped query is not emphasised: %s", got)
	}

	got = string(emphasiseMatch(`a <b>bold</b> line`, "absent"))
	if strings.Contains(got, "<b>") || strings.Contains(got, "<em") {
		t.Errorf("unmatched text was not escaped or was wrapped anyway: %s", got)
	}

	if got := string(emphasiseMatch("plain", "")); strings.Contains(got, "<em") {
		t.Errorf("an empty query emphasised something: %s", got)
	}
}

func TestMarkdownRewriteLinks(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("[dir](docs/personas)\n\n[dir2](docs/personas/)\n\n![img](assets/logo.png)\n\n[ext](https://example.com/x)\n\n[anchor](#sec)\n\n[md](docs/a.md#intro)\n\n[missing](nope/missing.txt)")
	rewrite := func(href string) string {
		switch href {
		case "docs/personas":
			return "/tree/HEAD/docs/personas/"
		case "assets/logo.png":
			return "/raw/HEAD/assets/logo.png"
		case "docs/a.md":
			return "/blob/HEAD/docs/a.md"
		}
		return ""
	}
	out := string(rd.Markdown(src, rewrite))
	checks := []string{
		`href="/tree/HEAD/docs/personas/"`,
		`src="/raw/HEAD/assets/logo.png"`,
		`href="https://example.com/x"`,      // external untouched
		`href="#sec"`,                       // anchor untouched
		`href="/blob/HEAD/docs/a.md#intro"`, // fragment preserved
		`href="nope/missing.txt"`,           // "" from rewrite keeps original
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("markdown output missing %q\nout: %s", c, out)
		}
	}
	if strings.Contains(out, `href="docs/personas"`) {
		t.Errorf("unrewritten relative link leaked:\n%s", out)
	}
}

func TestMarkdownSoftWrapsCollapse(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("line one\nline two\nline three\n\nnew paragraph\n")
	out := string(rd.Markdown(src, nil))
	if strings.Contains(out, "<br") {
		t.Errorf("soft newline rendered as hard break (GitHub collapses these):\n%s", out)
	}
	if strings.Count(out, "<p>") != 2 {
		t.Errorf("expected 2 paragraphs, got: %s", out)
	}
}

func TestTimeagoAndSizes(t *testing.T) {
	if got := timeagoText(time.Now().Add(-2 * time.Hour)); got != "2 hours ago" {
		t.Errorf("timeago hours: %q", got)
	}
	if got := timeagoText(time.Now().Add(90 * time.Second)); got != "in 1 minute" {
		t.Errorf("timeago future: %q", got)
	}
	if got := humanBytes(1536); got != "1.5 KB" {
		t.Errorf("humanBytes: %q", got)
	}
	if got := timeagoText(time.Now().Add(-30 * time.Second)); got != "just now" {
		t.Errorf("timeago seconds: %q", got)
	}
}

// TestTimeagoCarriesTheAbsoluteInstant: a relative time is a <time> element
// holding the exact instant, with UTC rendered server-side so the hover popover
// is useful without JavaScript.
func TestTimeagoCarriesTheAbsoluteInstant(t *testing.T) {
	if got := timeago(time.Time{}); got != "" {
		t.Errorf("zero time should render nothing, got %q", got)
	}
	stamp := time.Date(2026, 10, 18, 16, 12, 34, 0, time.UTC)
	got := string(timeago(stamp))
	for _, want := range []string{
		`<time class="time" datetime="2026-10-18T16:12:34Z">`,
		`<span class="time-local"></span>`,
		`<span class="time-utc">2026-10-18 16:12:34 UTC</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("timeago markup missing %q\ngot: %s", want, got)
		}
	}
}

func TestMarkdownTablesAreWrappedForScroll(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("| a | b |\n|---|---|\n| 1 | 2 |\n\nno table here\n")
	out := string(rd.Markdown(src, func(string) string { return "" }))

	if !strings.Contains(out, `<div class="table-scroll"><table>`) {
		t.Errorf("table not wrapped in a scroll container:\n%s", out)
	}
	if !strings.Contains(out, "</table></div>") {
		t.Errorf("table wrapper not closed:\n%s", out)
	}
	// every open wrapper must be closed, and no unwrapped table may remain
	if n := strings.Count(out, `<div class="table-scroll">`); n != strings.Count(out, "</table></div>") {
		t.Errorf("unbalanced table wrappers: %d open, %d closed", n, strings.Count(out, "</table></div>"))
	}
	if strings.Contains(out, "<table>") && !strings.Contains(out, `<div class="table-scroll"><table>`) {
		t.Errorf("unwrapped table leaked:\n%s", out)
	}
}

func TestVersionFontURLs(t *testing.T) {
	hashes := map[string]string{"fonts/a.woff2": "abc12345"}
	cases := []struct{ in, want string }{
		// every quoting style a stylesheet may use gets versioned, normalised
		// to double quotes so the served CSS is uniform
		{`src: url("/static/fonts/a.woff2") format("woff2");`, `src: url("/static/fonts/a.woff2?v=abc12345") format("woff2");`},
		{`src: url('/static/fonts/a.woff2') format("woff2");`, `src: url("/static/fonts/a.woff2?v=abc12345") format("woff2");`},
		{`src: url(/static/fonts/a.woff2) format("woff2");`, `src: url("/static/fonts/a.woff2?v=abc12345") format("woff2");`},
		{`src: url( "/static/fonts/a.woff2" );`, `src: url("/static/fonts/a.woff2?v=abc12345");`},
		// untouched: already versioned, unknown font, foreign path
		{`url("/static/fonts/a.woff2?v=old")`, `url("/static/fonts/a.woff2?v=old")`},
		{`url("/static/fonts/b.woff2")`, `url("/static/fonts/b.woff2")`},
		{`url("/uploads/x.woff2")`, `url("/uploads/x.woff2")`},
	}
	for _, c := range cases {
		if got := versionFontURLs(c.in, hashes); got != c.want {
			t.Errorf("versionFontURLs(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// TestWrapTablesLeavesCodeAlone: only real tables get the scroll wrapper. The
// literal text "<table>" inside a code span or a fence is escaped by goldmark
// and must survive unwrapped.
func TestWrapTablesLeavesCodeAlone(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("| a | b |\n|---|---|\n| 1 | 2 |\n\nInline `<table>` tag:\n\n```html\n<table><tr><td>x</td></tr></table>\n```\n")
	out := string(rd.Markdown(src, func(string) string { return "" }))

	if n := strings.Count(out, `<div class="table-scroll">`); n != 1 {
		t.Errorf("wrapped %d tables, want 1 (only the GFM table):\n%s", n, out)
	}
	if n := strings.Count(out, "<table>"); n != 1 {
		t.Errorf("found %d raw <table> tags, want 1 (escaped code must not match):\n%s", n, out)
	}
	if !strings.Contains(out, "&lt;table") {
		t.Errorf("escaped code containing <table> went missing:\n%s", out)
	}
}
