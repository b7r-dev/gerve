package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
)

// TestCommitPageLinksPinnedToCommit guards the fix for the review finding:
// "view file" (and every other ref-pinned link) on a commit page must point
// at the commit being viewed, not the default ref.
func TestCommitPageLinksPinnedToCommit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitHelper(t, dir, args...) }
	write := func(name, content string) {
		if err := os.WriteFile(dir+"/"+name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	write("file.txt", "v1\n")
	run("add", "-A")
	run("commit", "-q", "-m", "one")
	write("file.txt", "v1\nv2\n")
	run("add", "-A")
	run("commit", "-q", "-m", "two")
	sha := gitHelperOut(t, dir, "rev-parse", "HEAD")

	repo, err := git.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rnd, err := render.New()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(repo, rnd, Options{Version: "test"}).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/commit/" + sha)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("commit page: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)

	want := `href="/blob/` + sha + `/file.txt">view file`
	if !strings.Contains(page, want) {
		t.Errorf("view-file link not pinned to the viewed commit:\nwant %q", want)
	}
	if strings.Contains(page, `href="/blob/HEAD/`) {
		t.Errorf("link leaked the default ref")
	}
}
