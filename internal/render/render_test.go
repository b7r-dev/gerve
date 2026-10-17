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
		Version:   "test",
		ChromaCSS: "",
		RepoName:  "dotgrid",
		Ref:       "HEAD",
		Head:      git.HeadInfo{Branch: "main"},
		Refs:      []git.RefInfo{{Name: "main", Kind: "branch", Hash: "abc123def4567890123456789012345678901234"}},
		Route:     "tree",
		Path:      "",
		Crumbs:    []Crumb{{Name: "src", URL: "/tree/HEAD/src"}},
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
		"blob": BlobPage{Base: b, Blob: &git.Blob{Path: "main.go", Size: 10, Content: []byte("package main")}, Last: &c, Lines: 1, Code: "<div>code</div>", CanBlame: true, CanHistory: true},
		"blame": BlamePage{Base: b, Groups: []git.BlameGroup{{Commit: c, Lines: []git.BlameLine{{Num: 1, Text: "package main"}}}}},
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
		`href="https://example.com/x"`, // external untouched
		`href="#sec"`,                  // anchor untouched
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
	if got := timeago(time.Now().Add(-2 * time.Hour)); got != "2 hours ago" {
		t.Errorf("timeago hours: %q", got)
	}
	if got := timeago(time.Now().Add(90 * time.Second)); got != "in 1 minute" {
		t.Errorf("timeago future: %q", got)
	}
	if got := humanBytes(1536); got != "1.5 KB" {
		t.Errorf("humanBytes: %q", got)
	}
	if got := timeago(time.Now().Add(-30 * time.Second)); got != "just now" {
		t.Errorf("timeago seconds: %q", got)
	}
}
