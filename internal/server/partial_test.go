package server

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPartialClonePagesSayContentsAreNotAvailable: blame, diffs and search read
// file contents through git. In a clone that keeps them on a promisor remote the
// honest answer is "contents not available", not "untracked, binary, or too
// large" — and gerve must not fetch them just to avoid saying it.
func TestPartialClonePagesSayContentsAreNotAvailable(t *testing.T) {
	src := initTestRepo(t)
	commitFiles(t, src, "base", map[string]string{"f.txt": "one\n"})
	commitFiles(t, src, "change", map[string]string{"f.txt": "two\n"})
	gitHelper(t, src, "config", "uploadpack.allowFilter", "true")

	dst := filepath.Join(t.TempDir(), "clone")
	out, err := exec.Command("git", "clone", "-q", "--no-checkout",
		"--filter=blob:none", "file://"+src, dst).CombinedOutput()
	if err != nil {
		t.Skipf("blob-filtered clone unsupported here: %v: %s", err, out)
	}
	ts, _ := newTestServer(t, dst)

	for _, path := range []string{"/blame/main/f.txt", "/commit/HEAD", "/search/main?q=one"} {
		status, _, body := doReq(t, ts.URL+path, nil)
		if status != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500:\n%.600s", path, status, body)
			continue
		}
		if !strings.Contains(body, "never fetches missing objects") {
			t.Errorf("%s: the page does not say the contents are not local:\n%.600s", path, body)
		}
	}
}
