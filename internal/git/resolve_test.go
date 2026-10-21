package git

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sharedCloneFixture builds a repo with a merge, then clones it with --shared so
// every object lives in the original store through objects/info/alternates.
// go-git does not follow alternates, which is the situation that used to 404 a
// commit the log had just listed.
func sharedCloneFixture(t *testing.T) (src, dst string) {
	t.Helper()
	src = t.TempDir()
	run := func(args ...string) { gitOut(t, src, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	writeFile(t, src, "a.txt", "a\n")
	run("add", "-A")
	run("commit", "-q", "-m", "root")

	run("checkout", "-q", "-b", "feature")
	writeFile(t, src, "c.txt", "c\n")
	run("add", "-A")
	run("commit", "-q", "-m", "feature work")

	run("checkout", "-q", "main")
	writeFile(t, src, "b.txt", "b\n")
	run("add", "-A")
	run("commit", "-q", "-m", "main work")
	run("merge", "-q", "--no-ff", "feature", "-m", "merge feature\n\nbody line")
	run("tag", "-a", "v1", "-m", "tag v1")

	dst = filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", "--shared", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("clone --shared: %v: %s", err, out)
	}
	return src, dst
}

// TestResolveFallsBackToTheCLI: in a shared clone go-git cannot read any of the
// history, so resolution and commit metadata have to come from git. Without the
// fallback every hash the commits page links 404s.
func TestResolveFallsBackToTheCLI(t *testing.T) {
	_, dst := sharedCloneFixture(t)
	r, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	head, err := r.HeadHash()
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.Resolve(head)
	if err != nil {
		t.Fatalf("resolve full hash: %v", err)
	}
	if got != head {
		t.Errorf("resolve full hash = %s, want %s", got, head)
	}

	// An abbreviated hash is go-git's other blind spot; git answers it.
	if got, err := r.Resolve(head[:8]); err != nil || got != head {
		t.Errorf("resolve short hash = %s, %v; want %s", got, err, head)
	}

	// An annotated tag must peel to its commit, as the go-git path does.
	if got, err := r.Resolve("v1"); err != nil || got != head {
		t.Errorf("resolve annotated tag = %s, %v; want %s", got, err, head)
	}

	m, err := r.Commit(head)
	if err != nil {
		t.Fatalf("commit metadata: %v", err)
	}
	if m.Subject != "merge feature" {
		t.Errorf("subject = %q", m.Subject)
	}
	if !strings.Contains(m.Body, "body line") {
		t.Errorf("body = %q", m.Body)
	}
	if len(m.Parents) != 2 {
		t.Errorf("parents = %v, want 2", m.Parents)
	}
	if m.Author != "Test" {
		t.Errorf("author = %q", m.Author)
	}
	if m.Hash != head || m.Short != head[:7] {
		t.Errorf("hash/short = %s/%s", m.Hash, m.Short)
	}
	if m.Time.IsZero() {
		t.Error("commit time was not read")
	}

	// A revision that does not exist fails exactly as before, and a revision
	// that looks like a flag is not treated as one.
	for _, rev := range []string{"no-such-ref", "--help", "-n"} {
		if _, err := r.Resolve(rev); !errors.Is(err, ErrNotFound) {
			t.Errorf("resolve %q: err = %v, want ErrNotFound", rev, err)
		}
	}
}
