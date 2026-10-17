package git

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenBare requires GERVE_TEST_BARE to point at a bare repository.
func TestOpenBare(t *testing.T) {
	path := os.Getenv("GERVE_TEST_BARE")
	if path == "" {
		t.Skip("GERVE_TEST_BARE not set")
	}
	r, err := Open(path)
	if err != nil {
		t.Fatalf("open bare: %v", err)
	}
	if r.HasWorktree() {
		t.Error("bare repo reported a worktree")
	}
	if _, err := r.Resolve("HEAD"); err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}
	entries, err := r.ListDir(t.Context(), mustHead(t, r), "")
	if err != nil {
		t.Fatalf("list dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("bare repo tree empty")
	}
}

func mustHead(t *testing.T, r *Repo) string {
	t.Helper()
	h, err := r.HeadHash()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestBareDetection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"HEAD", "config"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !looksLikeBareGitDir(dir) {
		t.Error("expected bare gitdir detection")
	}
	if looksLikeBareGitDir(t.TempDir()) {
		t.Error("empty dir must not look like a bare gitdir")
	}
}
