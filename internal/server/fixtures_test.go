package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
)

// Shared fixtures for the server tests: every test needs a throwaway repo with
// an identity configured, then a server in front of it.

// initTestRepo creates an empty repo with a commit identity configured.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitHelper(t, dir, "init", "-q", "-b", "main")
	gitHelper(t, dir, "config", "user.email", "test@example.com")
	gitHelper(t, dir, "config", "user.name", "Test")
	return dir
}

// commitFiles writes files into dir and commits them, returning the new HEAD.
func commitFiles(t *testing.T, dir, subject string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitHelper(t, dir, "add", "-A")
	gitHelper(t, dir, "commit", "-q", "-m", subject)
	return gitHelperOut(t, dir, "rev-parse", "HEAD")
}

// newTestServer serves dir and returns the server plus its renderer. The server
// is closed when the test ends.
func newTestServer(t *testing.T, dir string) (*httptest.Server, *render.Renderer) {
	t.Helper()
	repo, err := git.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rnd, err := render.New()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(repo, rnd, Options{Version: "test"}).Handler())
	t.Cleanup(ts.Close)
	return ts, rnd
}
