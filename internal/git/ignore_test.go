package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorktreeOnlyDirHidesIgnoredChildren pins the rule for directories that
// exist only in the worktree: they are listed from disk, every row is genuinely
// untracked, and the children the ignore rules cover are counted but never
// listed or served.
//
// The rows that were shown before this rule existed were both wrong and dead:
// git status never reports an ignored path, so it could not be fetched, and the
// listing guessed "untracked" for it anyway.
func TestWorktreeOnlyDirHidesIgnoredChildren(t *testing.T) {
	// A developer's global excludes would otherwise change the answer.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)

	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	writeFile(t, dir, ".gitignore", "*.secret\nbuild/\n")
	run("add", "-A")
	run("commit", "-q", "-m", "ignore rules")

	for _, d := range []string{"scratch/build", "scratch/sub"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "scratch/keep.txt", "keep\n")
	writeFile(t, dir, "scratch/notes.md", "notes\n")
	writeFile(t, dir, "scratch/api.secret", "token\n")
	writeFile(t, dir, "scratch/build/artifact.o", "not counted separately\n")
	writeFile(t, dir, "scratch/sub/inner.txt", "inner\n")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	head := mustHead(t, r)

	listing, err := r.ListDir(t.Context(), head, "scratch")
	if err != nil {
		t.Fatalf("list scratch: %v", err)
	}
	var names []string
	for _, e := range listing.Entries {
		names = append(names, e.Name)
		if e.Change != Untracked {
			t.Errorf("%s: change = %q, want untracked", e.Name, e.Change)
		}
	}
	// Directories first, then names. api.secret is gone and build is one
	// hidden entry — its contents are not counted again.
	if got, want := strings.Join(names, ","), "sub,keep.txt,notes.md"; got != want {
		t.Errorf("entries = %q, want %q", got, want)
	}
	if listing.Ignored != 2 {
		t.Errorf("Ignored = %d, want 2 (api.secret and build)", listing.Ignored)
	}

	// Nothing ignored is in the status map, which is why the old listing had
	// to guess — and why Blob refused to serve it.
	status := r.Status(t.Context())
	if _, ok := status["scratch/api.secret"]; ok {
		t.Error("an ignored path appeared in status; the listing no longer has to guess")
	}

	// The contract: what is listed is servable, what is hidden is not.
	b, err := r.Blob(t.Context(), head, "scratch/keep.txt", true)
	if err != nil {
		t.Fatalf("listed file is not servable: %v", err)
	}
	if !b.FromWorktree {
		t.Error("listed worktree-only file was not read from disk")
	}
	if _, err := r.Blob(t.Context(), head, "scratch/api.secret", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("ignored file served: err = %v, want ErrNotFound", err)
	}

	// A directory with nothing ignored reports zero, so the page can omit the
	// note entirely.
	nested, err := r.ListDir(t.Context(), head, "scratch/sub")
	if err != nil {
		t.Fatalf("list scratch/sub: %v", err)
	}
	if nested.Ignored != 0 || len(nested.Entries) != 1 || nested.Entries[0].Name != "inner.txt" {
		t.Errorf("nested listing = %+v, want one entry and no hidden ones", nested)
	}
}
