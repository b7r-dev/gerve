package server

import (
	"strings"
	"testing"
)

// TestCommitPageLinksPinnedToCommit guards the fix for the review finding:
// "view file" (and every other ref-pinned link) on a commit page must point at
// the commit being viewed, not the default ref — and must say which revision it
// opens, since the page pins itself to that commit rather than HEAD.
func TestCommitPageLinksPinnedToCommit(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	sha := commitFiles(t, dir, "two", map[string]string{"file.txt": "v1\nv2\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/commit/"+sha)
	// shortHash shows the first 7 characters, as everywhere else in the UI
	want := `href="/blob/` + sha + `/file.txt">view file @ ` + sha[:7] + ` &rarr;`
	if !strings.Contains(body, want) {
		t.Errorf("view-file link not pinned and labelled to the viewed commit:\nwant %q", want)
	}
	if strings.Contains(body, `href="/blob/HEAD/`) {
		t.Error("link leaked the default ref")
	}
}
