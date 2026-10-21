package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// compareFixture builds two diverged branches:
//
//	base      a.txt, gone.txt, old.txt, img.bin
//	feature   c.txt added, old.txt renamed to renamed.txt, gone.txt deleted
//	main      baseonly.txt added after the split
//
// The split matters: three-dot comparison must not show baseonly.txt, and it
// must show the merge base rather than main's tip.
func compareFixture(t *testing.T) (r *Repo, base, head string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	writeFile(t, dir, "gone.txt", "delete me\n")
	writeFile(t, dir, "old.txt", "rename me\nwith a second line\n")
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	split := gitOut(t, dir, "rev-parse", "HEAD")

	run("checkout", "-q", "-b", "feature")
	writeFile(t, dir, "c.txt", "new file\n")
	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "renamed.txt", "rename me\nwith a second line\nand a third\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img.bin"), []byte{0, 9, 9, 9}, 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "feature changes")
	head = gitOut(t, dir, "rev-parse", "HEAD")

	run("checkout", "-q", "main")
	writeFile(t, dir, "baseonly.txt", "not part of the branch\n")
	run("add", "-A")
	run("commit", "-q", "-m", "main moves on")
	base = gitOut(t, dir, "rev-parse", "HEAD")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if split == "" {
		t.Fatal("no split commit")
	}
	return repo, base, head
}

func findFile(files []RangeFile, newPath, oldPath string) *RangeFile {
	for i := range files {
		if files[i].New == newPath && files[i].Old == oldPath {
			return &files[i]
		}
	}
	return nil
}

func TestCompareUsesTheMergeBase(t *testing.T) {
	r, base, head := compareFixture(t)
	rng, err := r.Compare(t.Context(), base, head, "")
	if err != nil {
		t.Fatal(err)
	}

	if rng.MergeBase == nil {
		t.Fatal("no merge base recorded")
	}
	if rng.MergeBase.Subject != "base" {
		t.Errorf("merge base = %q, want the split commit", rng.MergeBase.Subject)
	}
	if rng.Ahead != 1 || rng.Behind != 1 {
		t.Errorf("ahead/behind = %d/%d, want 1/1", rng.Ahead, rng.Behind)
	}
	if len(rng.Commits) != 1 || rng.Commits[0].Hash != head {
		t.Errorf("commit list = %+v, want just the feature commit", rng.Commits)
	}

	// Three-dot: main's own commit must not appear as a change.
	if f := findFile(rng.Files, "baseonly.txt", ""); f != nil {
		t.Error("the base branch's own file is in the diff; that is two-dot behaviour")
	}
	if f := findFile(rng.Files, "c.txt", ""); f == nil || f.Status != "A" {
		t.Errorf("c.txt = %+v, want an addition", f)
	}
	if f := findFile(rng.Files, "renamed.txt", "old.txt"); f == nil || f.Status != "R" {
		t.Errorf("rename = %+v, want R old.txt -> renamed.txt", f)
	} else if f.Adds != 1 {
		t.Errorf("rename adds = %d, want 1", f.Adds)
	}
	if f := findFile(rng.Files, "", "gone.txt"); f == nil || f.Status != "D" {
		t.Errorf("deletion = %+v, want D", f)
	}
	if f := findFile(rng.Files, "img.bin", ""); f == nil || !f.Binary {
		t.Errorf("img.bin = %+v, want binary", f)
	}
	if rng.Adds == 0 || rng.Dels == 0 {
		t.Errorf("totals = +%d/-%d, want both non-zero", rng.Adds, rng.Dels)
	}

	// Path scope narrows the stat pass, and the patches follow from it.
	scoped, err := r.Compare(t.Context(), base, head, "c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Files) != 1 || scoped.Files[0].New != "c.txt" {
		t.Errorf("scoped files = %+v, want just c.txt", scoped.Files)
	}
}

func TestRangePatchRendersTheFilesItLists(t *testing.T) {
	r, base, head := compareFixture(t)
	rng, err := r.Compare(t.Context(), base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	patches := r.RangePatch(t.Context(), rng.From, head, rng.Files)
	if patches.Truncated || patches.Stopped {
		t.Errorf("unexpected truncation: %+v", patches)
	}
	for _, f := range rng.Files {
		if f.Binary {
			continue
		}
		d, ok := patches.ByKey[rangeKey(f.Old, f.New, f.Status)]
		if !ok {
			t.Errorf("%s/%s: no patch returned", f.Old, f.New)
			continue
		}
		if d.Patch == "" {
			t.Errorf("%s/%s: empty patch", f.Old, f.New)
		}
	}
	if d := patches.ByKey["old.txt\x00renamed.txt"]; d.Status != "R" {
		t.Errorf("rename patch status = %q, want R (both paths must be in the pathspec)", d.Status)
	}
}

func TestCompareCapsTheFileList(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	writeFile(t, dir, "a.txt", "a\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base := gitOut(t, dir, "rev-parse", "HEAD")

	run("checkout", "-q", "-b", "many")
	for i := 0; i < CompareMaxFiles+5; i++ {
		writeFile(t, dir, fmt.Sprintf("f%04d.txt", i), "x\n")
	}
	run("add", "-A")
	run("commit", "-q", "-m", "many files")
	head := gitOut(t, dir, "rev-parse", "HEAD")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rng, err := r.Compare(t.Context(), base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rng.Files) != CompareMaxFiles {
		t.Errorf("files = %d, want the cap %d", len(rng.Files), CompareMaxFiles)
	}
	if rng.FilesMore != 5 {
		t.Errorf("FilesMore = %d, want 5", rng.FilesMore)
	}
	if len(rng.Files)+rng.FilesMore != CompareMaxFiles+5 {
		t.Errorf("the total does not add up: %d + %d", len(rng.Files), rng.FilesMore)
	}
}

func TestRangePatchCapsAHugeFile(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	old := strings.Repeat("old line\n", 200_000)
	if err := os.WriteFile(filepath.Join(dir, "huge.txt"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base := gitOut(t, dir, "rev-parse", "HEAD")

	newer := strings.Repeat("new line\n", 200_000)
	if err := os.WriteFile(filepath.Join(dir, "huge.txt"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "huge rewrite")
	head := gitOut(t, dir, "rev-parse", "HEAD")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rng, err := r.Compare(t.Context(), base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	patches := r.RangePatch(t.Context(), rng.From, head, rng.Files)
	if !patches.Truncated {
		t.Error("a patch over the per-file cap was not reported as truncated")
	}
	if _, ok := patches.ByKey[rangeKey("", "huge.txt", "M")]; ok {
		t.Error("a patch over the per-file cap was rendered anyway")
	}
}

// TestCompareRefusesToFetchInAPartialClone: gerve is zero-egress, so a diff whose
// objects live on a promisor remote must fail with the honest sentinel instead of
// fetching them to look complete.
func TestCompareRefusesToFetchInAPartialClone(t *testing.T) {
	src := t.TempDir()
	run := func(args ...string) { gitOut(t, src, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	writeFile(t, src, "f.txt", "one\ntwo\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base := gitOut(t, src, "rev-parse", "HEAD")
	writeFile(t, src, "f.txt", "one\nchanged\n")
	run("add", "-A")
	run("commit", "-q", "-m", "change")
	head := gitOut(t, src, "rev-parse", "HEAD")
	run("config", "uploadpack.allowFilter", "true")

	dst := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", "--filter=blob:none", "file://"+src, dst).CombinedOutput(); err != nil {
		t.Skipf("partial clone unsupported here: %v: %s", err, out)
	}
	// Removing the remote proves nothing can be fetched: whatever is missing now
	// has nowhere to come from.
	gitOut(t, dst, "remote", "remove", "origin")

	r, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Compare(t.Context(), base, head, ""); !errors.Is(err, ErrObjectsUnavailable) {
		t.Fatalf("err = %v, want ErrObjectsUnavailable", err)
	}
}

func TestCompareWithoutAMergeBase(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	writeFile(t, dir, "a.txt", "a\n")
	run("add", "-A")
	run("commit", "-q", "-m", "first history")

	run("checkout", "-q", "--orphan", "other")
	run("rm", "-q", "-rf", "--cached", ".")
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "b.txt", "b\n")
	run("add", "-A")
	run("commit", "-q", "-m", "second history")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base, err := r.Resolve("main")
	if err != nil {
		t.Fatal(err)
	}
	head, err := r.Resolve("other")
	if err != nil {
		t.Fatal(err)
	}

	rng, err := r.Compare(t.Context(), base, head, "")
	if err != nil {
		t.Fatalf("unrelated histories must degrade, not fail: %v", err)
	}
	if rng.MergeBase != nil {
		t.Errorf("merge base = %v, want none", rng.MergeBase)
	}
	if rng.From != base {
		t.Errorf("From = %s, want the base tree when there is no ancestor", rng.From)
	}
	if len(rng.Files) != 2 {
		t.Errorf("tree-to-tree files = %d, want 2 (a deleted, b added)", len(rng.Files))
	}

	self, err := r.Compare(t.Context(), head, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(self.Files) != 0 || len(self.Commits) != 0 || self.MergeBase == nil {
		t.Errorf("self-compare = %+v, want no files and a merge base", self)
	}

	// A cancelled context returns rather than running the whole range — and it
	// says the answer was cut short, so the page cannot claim "no changes".
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cut, err := r.Compare(ctx, base, head, "")
	if err != nil {
		t.Errorf("cancelled compare returned an error: %v", err)
	}
	if cut != nil && !cut.Stopped {
		t.Error("a cancelled comparison did not report Stopped")
	}
}

// TestCompareParsesStatRecords pins the two NUL-separated formats against the
// shapes that bite: a rename, a binary file, a path with a tab, a leading colon,
// and a truncated record that must not panic.
func TestCompareParsesStatRecords(t *testing.T) {
	ns := parseNameStatusZ([]byte("M\x00plain.txt\x00R073\x00old.txt\x00new.txt\x00A\x00:colon.txt\x00D\x00gone.txt\x00"))
	wantNS := []statusRec{
		{status: "M", new: "plain.txt"},
		{status: "R", old: "old.txt", new: "new.txt"},
		{status: "A", new: ":colon.txt"},
		{status: "D", old: "gone.txt"},
	}
	if !reflect.DeepEqual(ns, wantNS) {
		t.Errorf("name-status = %+v, want %+v", ns, wantNS)
	}

	nums := parseNumstatZ([]byte("1\t2\tplain.txt\x001\t1\t\x00old.txt\x00new.txt\x00-\t-\timg.bin\x001\t3\tpa\tth.txt\x00"))
	wantNums := []numstatRec{
		{adds: 1, dels: 2, new: "plain.txt"},
		{adds: 1, dels: 1, old: "old.txt", new: "new.txt"},
		{binary: true, new: "img.bin"},
		{adds: 1, dels: 3, new: "pa\tth.txt"},
	}
	if !reflect.DeepEqual(nums, wantNums) {
		t.Errorf("numstat = %+v, want %+v", nums, wantNums)
	}

	// A rename record cut short yields nothing rather than a half file.
	if got := parseNameStatusZ([]byte("R073\x00old.txt\x00")); len(got) != 0 {
		t.Errorf("truncated rename parsed as %+v", got)
	}
}

// TestCompareTreatsColonPathsAsData: without --literal-pathspecs git reads a
// leading ":" as pathspec magic, and the file silently vanishes from a diff.
func TestCompareTreatsColonPathsAsData(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	writeFile(t, dir, "plain.txt", "one\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base := gitOut(t, dir, "rev-parse", "HEAD")

	run("checkout", "-q", "-b", "feature")
	writeFile(t, dir, ":colon.txt", "colon\n")
	run("add", "-A")
	run("commit", "-q", "-m", "colon file")
	head := gitOut(t, dir, "rev-parse", "HEAD")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rng, err := r.Compare(t.Context(), base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rng.Files {
		if f.New == ":colon.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a path beginning with ':' vanished from the diff: %+v", rng.Files)
	}
	patches := r.RangePatch(t.Context(), rng.From, head, rng.Files)
	if _, ok := patches.Get(RangeFile{Status: "A", New: ":colon.txt"}); !ok {
		t.Error("the colon path was listed but its patch was not fetched")
	}
}

// TestGitLimitStopsAtTheCap: the cap is enforced while git is writing, not after
// the whole output has been buffered, which is the only way the page's memory is
// bounded rather than its display.
func TestGitLimitStopsAtTheCap(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	for i := 0; i < 5; i++ {
		writeFile(t, dir, "f.txt", strings.Repeat("x\n", 100+i))
		run("add", "-A")
		run("commit", "-q", "-m", "c")
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.gitLimit(t.Context(), nil, 8, "log", "--format=%H"); !errors.Is(err, errOutputTooLarge) {
		t.Fatalf("err = %v, want errOutputTooLarge", err)
	}
	out, err := r.gitLimit(t.Context(), nil, 1<<20, "log", "--format=%H")
	if err != nil || len(out) == 0 {
		t.Fatalf("under the cap: %d bytes, %v", len(out), err)
	}
}
