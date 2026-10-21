package server

import (
	"net/http"
	"strings"
	"testing"
)

// TestTreeHidesIgnoredChildrenOfAWorktreeOnlyDir covers the page as a reader
// sees it: a directory that exists only in the worktree lists its files, says
// how many entries the ignore rules kept out, and does not link to any of them.
//
// The count is the point. Hiding without it would make the listing look
// complete when it is not, and the whole reason for hiding is that the files
// people exclude are usually the ones that must never be served.
func TestTreeHidesIgnoredChildrenOfAWorktreeOnlyDir(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "ignore rules", map[string]string{
		".gitignore": "*.secret\nbuild/\n",
	})
	writeWorktreeFile(t, dir, "scratch/keep.txt", "keep\n")
	writeWorktreeFile(t, dir, "scratch/api.secret", "token\n")
	writeWorktreeFile(t, dir, "scratch/build/artifact.o", "artifact\n")
	writeWorktreeFile(t, dir, "scratch/sub/inner.txt", "inner\n")

	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/tree/HEAD/scratch/")
	for _, hidden := range []string{"api.secret", "artifact.o", "build/"} {
		if strings.Contains(body, hidden) {
			t.Errorf("ignored %q is in the listing", hidden)
		}
	}
	for _, shown := range []string{"keep.txt", "sub/"} {
		if !strings.Contains(body, shown) {
			t.Errorf("%q is not in the listing", shown)
		}
	}
	if !strings.Contains(body, "2 ignored entries hidden") {
		t.Errorf("the listing does not say what it left out:\n%.1200s", body)
	}

	// The hidden path is not reachable either — hiding it in the listing while
	// serving it would be worse than showing it.
	if status, _, _ := doReq(t, ts.URL+"/blob/HEAD/scratch/api.secret", nil); status != http.StatusNotFound {
		t.Errorf("ignored file served: status = %d, want 404", status)
	}
	if status, _, _ := doReq(t, ts.URL+"/raw/HEAD/scratch/api.secret", nil); status != http.StatusNotFound {
		t.Errorf("ignored file served raw: status = %d, want 404", status)
	}

	// And the files that are listed still open.
	if status, _, _ := doReq(t, ts.URL+"/blob/HEAD/scratch/keep.txt", nil); status != http.StatusOK {
		t.Errorf("listed file did not open: status = %d, want 200", status)
	}
}

// TestTreeDoesNotCallAnIgnoredOnlyDirectoryEmpty: a directory whose only
// contents are ignored is not empty, and the page should not pretend it is.
func TestTreeDoesNotCallAnIgnoredOnlyDirectoryEmpty(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "ignore rules", map[string]string{
		".gitignore": "*.secret\n",
	})
	writeWorktreeFile(t, dir, "vault/api.secret", "token\n")

	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/tree/HEAD/vault/")
	if strings.Contains(body, "empty directory") {
		t.Errorf("a directory with one hidden entry was called empty:\n%.1200s", body)
	}
	if !strings.Contains(body, "1 ignored entry hidden") {
		t.Errorf("the singular count did not render:\n%.1200s", body)
	}
	if strings.Contains(body, "api.secret") {
		t.Error("the ignored name is in the listing")
	}
}
