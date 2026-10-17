package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"b7r.dev/gerve/internal/git"
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
