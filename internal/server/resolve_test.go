package server

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeCommitOpensInASharedClone is the user-visible half of the resolver
// fallback: the commits page lists a hash, clicking it must not 404 because
// go-git cannot see objects kept in objects/info/alternates.
func TestMergeCommitOpensInASharedClone(t *testing.T) {
	src := initTestRepo(t)
	commitFiles(t, src, "root", map[string]string{"a.txt": "a\n"})
	gitHelper(t, src, "checkout", "-q", "-b", "feature")
	commitFiles(t, src, "feature work", map[string]string{"c.txt": "c\n"})
	gitHelper(t, src, "checkout", "-q", "main")
	commitFiles(t, src, "main work", map[string]string{"b.txt": "b\n"})
	gitHelper(t, src, "merge", "-q", "--no-ff", "feature", "-m", "merge feature")
	merge := gitHelperOut(t, src, "rev-parse", "HEAD")

	dst := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", "--shared", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("clone --shared: %v: %s", err, out)
	}
	ts, _ := newTestServer(t, dst)

	commits, _ := getBody(t, ts.URL+"/commits/HEAD")
	if !strings.Contains(commits, "/commit/"+merge) {
		t.Fatalf("the commits page does not link the merge commit:\n%.800s", commits)
	}

	status, _, body := doReq(t, ts.URL+"/commit/"+merge, nil)
	if status != http.StatusOK {
		t.Fatalf("commit page status = %d, want 200:\n%.800s", status, body)
	}
	if strings.Contains(body, "No commit named") {
		t.Errorf("the page still claims the commit is missing:\n%.800s", body)
	}
	if !strings.Contains(body, "merge feature") {
		t.Errorf("the commit subject is not on the page:\n%.800s", body)
	}

	// The fallback must not turn "no such commit" into a success.
	if status, _, _ := doReq(t, ts.URL+"/commit/deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", nil); status != http.StatusNotFound {
		t.Errorf("unknown commit status = %d, want 404", status)
	}
}
