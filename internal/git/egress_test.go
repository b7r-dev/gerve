package git

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// treelessClone builds a source repo with two commits and clones it without any
// trees, keeping the promisor remote. A read that fetches adds a pack to the
// clone, so the pack listing is the only proof of silence worth asserting.
func treelessClone(t *testing.T) (dst, head string) {
	t.Helper()
	src := t.TempDir()
	run := func(args ...string) { gitOut(t, src, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	writeFile(t, src, "f.txt", "one\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	writeFile(t, src, "f.txt", "one\ntwo\n")
	run("add", "-A")
	run("commit", "-q", "-m", "change")
	run("config", "uploadpack.allowFilter", "true")

	dst = filepath.Join(t.TempDir(), "clone")
	out, err := exec.Command("git", "clone", "-q", "--no-checkout",
		"--filter=tree:0", "file://"+src, dst).CombinedOutput()
	if err != nil {
		t.Skipf("tree-filtered clone unsupported here: %v: %s", err, out)
	}
	return dst, gitOut(t, dst, "rev-parse", "HEAD")
}

func countPromisorPacks(t *testing.T, repo string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(repo, ".git", "objects", "pack", "*.promisor"))
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

// TestGitReadsNeverFetchInAPartialClone pins zero egress at the lowest level:
// the guard belongs to runGitTo, so a new call site cannot forget it. The
// promisor remote stays reachable — removing it would hide a fetch rather than
// prevent one.
func TestGitReadsNeverFetchInAPartialClone(t *testing.T) {
	dst, head := treelessClone(t)
	r, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	before := countPromisorPacks(t, dst)

	// A path lookup needs the root tree, which a tree:0 clone does not have.
	if _, err := r.git(t.Context(), "cat-file", "-e", head+":f.txt"); err == nil {
		t.Error("the read succeeded: the tree was fetched")
	}
	if got := countPromisorPacks(t, dst); got != before {
		t.Errorf("promisor packs = %d after a read, want %d: git fetched", got, before)
	}

	// The exported helper goes through the same door.
	if r.ExistsAt(t.Context(), head, "f.txt") {
		t.Error("ExistsAt claimed a path whose tree is not local")
	}
	if got := countPromisorPacks(t, dst); got != before {
		t.Errorf("promisor packs = %d after ExistsAt, want %d: git fetched", got, before)
	}
}

// TestNoLazyFetchReplacesAnInheritedValue: inheriting GIT_NO_LAZY_FETCH=0 from
// the shell must not turn the promise into an option.
func TestNoLazyFetchReplacesAnInheritedValue(t *testing.T) {
	env := withNoLazyFetch([]string{"PATH=/bin", noLazyFetchKey + "=0"})
	var got []string
	for _, kv := range env {
		if strings.HasPrefix(kv, noLazyFetchKey+"=") {
			got = append(got, kv)
		}
	}
	if len(got) != 1 || got[0] != noLazyFetchKey+"=1" {
		t.Errorf("guard entries = %v, want exactly %s=1", got, noLazyFetchKey)
	}
}

// TestPromisorPacksFindsTheCommonObjectStore: in a linked worktree .git is a
// file, so the pack marker has to be looked up where git says the objects are.
func TestPromisorPacksFindsTheCommonObjectStore(t *testing.T) {
	dst, _ := treelessClone(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dst, "worktree", "add", "-q", "--no-checkout", wt)

	r := &Repo{root: wt, worktree: true}
	if !r.promisorPacks() {
		t.Error("a linked worktree of a partial clone did not report promisor packs")
	}
}
