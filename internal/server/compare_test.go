package server

import (
	"net/http"
	"strings"
	"testing"
)

// compareServerFixture builds main and feature apart from a common base, so the
// three-dot diff has something to exclude as well as something to show.
func compareServerFixture(t *testing.T) string {
	t.Helper()
	dir := initTestRepo(t)
	commitFiles(t, dir, "base", map[string]string{
		"a.txt":    "a\n",
		"gone.txt": "delete me\n",
		"old.txt":  "rename me\n",
	})
	gitHelper(t, dir, "checkout", "-q", "-b", "feature")
	commitFiles(t, dir, "feature work", map[string]string{"c.txt": "c\n"})
	gitHelper(t, dir, "mv", "old.txt", "renamed.txt")
	gitHelper(t, dir, "rm", "-q", "gone.txt")
	gitHelper(t, dir, "commit", "-q", "-am", "rename and delete")
	gitHelper(t, dir, "checkout", "-q", "main")
	commitFiles(t, dir, "main moves on", map[string]string{"baseonly.txt": "not the branch\n"})
	return dir
}

func TestComparePageShowsTheRange(t *testing.T) {
	ts, _ := newTestServer(t, compareServerFixture(t))

	status, _, body := doReq(t, ts.URL+"/compare/main...feature", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%.1200s", status, body)
	}
	for _, want := range []string{"changes from", "merge base", "c.txt", "renamed.txt", "gone.txt", `id="file-1"`, "swap base and head"} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if strings.Contains(body, "baseonly.txt") {
		t.Error("the base branch's own file is in the diff; that is two-dot behaviour")
	}
	if strings.Contains(body, `id="refswitch"`) {
		t.Error("the ref switcher is on a compare page; it would navigate to /compare/<one-ref>")
	}

	// Side-by-side is the same data, a different renderer.
	_, _, side := doReq(t, ts.URL+"/compare/main...feature?view=side", nil)
	if !strings.Contains(side, `class="diff side"`) {
		t.Error("view=side did not render paired hunks")
	}

	// Scope prunes the file list and says so.
	_, _, scoped := doReq(t, ts.URL+"/compare/main...feature?path=c.txt", nil)
	if !strings.Contains(scoped, "scoped to") || strings.Contains(scoped, "renamed.txt") {
		t.Error("the path scope was not applied or not stated")
	}
}

func TestCompareURLForms(t *testing.T) {
	ts, _ := newTestServer(t, compareServerFixture(t))

	// The picker's form is a plain GET, so it arrives as a query; assert the
	// canonical redirect without following it.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/compare/?base=main&head=feature")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("picker form status = %d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/compare/main...feature" {
		t.Errorf("Location = %q, want the canonical path", got)
	}

	status, _, body := doReq(t, ts.URL+"/compare/", nil)
	if status != http.StatusOK || !strings.Contains(body, `class="compareform"`) {
		t.Errorf("picker did not render: status %d", status)
	}

	// Three dots or nothing: two-dot means tree-to-tree, which this page does
	// not offer, and saying so beats silently showing a different diff.
	if status, _, _ := doReq(t, ts.URL+"/compare/main..feature", nil); status != http.StatusBadRequest {
		t.Errorf("two-dot status = %d, want 400", status)
	}
	if status, _, _ := doReq(t, ts.URL+"/compare/nope...feature", nil); status != http.StatusNotFound {
		t.Errorf("unknown base status = %d, want 404", status)
	}
	if status, _, _ := doReq(t, ts.URL+"/compare/main...nope", nil); status != http.StatusNotFound {
		t.Errorf("unknown head status = %d, want 404", status)
	}
}

func TestCompareSlashedRefsAndSelfCompare(t *testing.T) {
	dir := compareServerFixture(t)
	gitHelper(t, dir, "checkout", "-q", "-b", "team/webhooks")
	commitFiles(t, dir, "slash branch", map[string]string{"x.txt": "x\n"})
	ts, _ := newTestServer(t, dir)

	if status, _, body := doReq(t, ts.URL+"/compare/main...team/webhooks", nil); status != http.StatusOK {
		t.Fatalf("slashed refs: status = %d, want 200:\n%.800s", status, body)
	}

	_, _, self := doReq(t, ts.URL+"/compare/main...main", nil)
	if !strings.Contains(self, "no changes between these refs") {
		t.Error("a self-comparison is not reported as having no changes")
	}
}

func TestComparePageRevalidates(t *testing.T) {
	ts, _ := newTestServer(t, compareServerFixture(t))
	status, hdr, _ := doReq(t, ts.URL+"/compare/main...feature", nil)
	if status != http.StatusOK {
		t.Fatalf("first request: %d", status)
	}
	tag := hdr.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag on a compare page")
	}
	status, _, body := doReq(t, ts.URL+"/compare/main...feature", map[string]string{"If-None-Match": tag})
	if status != http.StatusNotModified || body != "" {
		t.Errorf("revalidation: status = %d, body = %q; want 304", status, body)
	}
}

func TestCompareEntryPoints(t *testing.T) {
	ts, _ := newTestServer(t, compareServerFixture(t))
	for _, path := range []string{"/refs", "/commits/main"} {
		_, _, body := doReq(t, ts.URL+path, nil)
		if !strings.Contains(body, `class="compareform"`) {
			t.Errorf("%s has no compare form", path)
		}
	}
}
