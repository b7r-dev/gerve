package server

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func gitHelper(t *testing.T, dir string, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %s: %s", args, strings.TrimSpace(errb.String()), err)
	}
}

func gitHelperOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %s: %s", args, strings.TrimSpace(errb.String()), err)
	}
	return strings.TrimSpace(out.String())
}

func TestRepoRelative(t *testing.T) {
	cases := []struct {
		base, href, want string
		ok               bool
	}{
		{"", "docs/personas", "docs/personas", true},
		{"", "docs/personas/", "docs/personas", true}, // trailing slash collapses
		{"", "./README.md", "README.md", true},        // explicit current dir
		{"docs", "personas/00.md", "docs/personas/00.md", true},
		{"a/b", "../c.md", "a/c.md", true},       // one level up
		{"a/b", "../../top.md", "top.md", true},  // up to root
		{"a/b", "../../../escape.md", "", false}, // escapes the repo
		{"", "/root.md", "root.md", true},        // root-relative
		{"", ".git/config", "", false},           // .git forbidden
		{"", "a/../../.git/config", "", false},
		{"", "", "", false},
		{"", "file.md?download=1", "file.md", true}, // query stripped
		{"", "my%20file.md", "my file.md", true},    // percent-decoded
	}
	for _, c := range cases {
		got, ok := repoRelative(c.base, c.href)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("repoRelative(%q, %q) = (%q, %v), want (%q, %v)",
				c.base, c.href, got, ok, c.want, c.ok)
		}
	}
}

// TestBlameRowsAreAddressable: blame groups are code rows like any other, so a
// blamed line can be linked to as well. The anchor is the file's line number,
// which is what a reader of blame means by "line 12" whichever commit owns it —
// the group header above it already says whose line it is.
func TestBlameRowsAreAddressable(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "a\nb\nc\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/blame/HEAD/file.txt")
	for _, want := range []string{`id="L1"`, `id="L2"`, `id="L3"`} {
		if !strings.Contains(body, want) {
			t.Errorf("blame page missing anchor %s", want)
		}
	}
	if n := strings.Count(body, `<tr id="L`); n != 3 {
		t.Errorf("blame rows = %d, want 3 (one per blamed line)", n)
	}
}

// TestBlameAttributionSpansRun: the contributor and hash are spelled once for a
// contiguous run of lines, not once per line. Two commits touch the file, so
// there are two labels for its four lines; each label spans its run with
// rowspan and the code sits in its own column.
func TestBlameAttributionSpansRun(t *testing.T) {
	dir := initTestRepo(t)
	first := commitFiles(t, dir, "one", map[string]string{"file.txt": "a\nb\nc\n"})
	second := commitFiles(t, dir, "two", map[string]string{"file.txt": "a\nb\nc\nd\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/blame/HEAD/file.txt")
	if n := strings.Count(body, `class="bwho"`); n != 2 {
		t.Errorf("attribution cells = %d, want 2 (one per commit run)", n)
	}
	for _, hash := range []string{first, second} {
		if want := `href="/commit/` + hash + `"`; !strings.Contains(body, want) {
			t.Errorf("blame page missing attribution link %q", want)
		}
	}
	// Four lines, two runs: the first run spans three rows, the second one.
	for _, want := range []string{`rowspan="3"`, `rowspan="1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("blame page missing %s", want)
		}
	}
	if n := strings.Count(body, `<tr id="L`); n != 4 {
		t.Errorf("blame rows = %d, want 4 (one per blamed line)", n)
	}
}
