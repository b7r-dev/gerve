package git

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// searchFixture builds a repo whose searchable content covers the cases that
// decide whether the parser and the overlay split are right:
//
//	committed.txt   tracked, later modified in the worktree without committing
//	weird:name.txt  a path containing a colon, where the host filesystem allows it
//	tricky.txt      a line whose text looks like "42:something"
//	sub/spaced.txt  a path containing a space
//	bin.dat         binary, must never appear
//	ignored.txt     gitignored, must never appear
//	fresh.txt       exists only in the worktree, must appear and be marked
func searchFixture(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }

	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	writeFile(t, dir, "committed.txt", "committed-needle here\nsecond line\n")
	if supportsUnusualFilenames() {
		writeFile(t, dir, "weird:name.txt", "colon-needle here\n")
	}
	writeFile(t, dir, "tricky.txt", "42:needle-shaped text\nneedle here\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "sub/spaced.txt", "spaced-needle here\n")
	writeFile(t, dir, "binary.dat", "needle\x00needle\n")
	writeFile(t, dir, "ignored.txt", "ignored-needle here\n")
	writeFile(t, dir, ".gitignore", "ignored.txt\n")
	run("add", "-A")
	run("commit", "-q", "-m", "one")

	// Uncommitted: a modified tracked file, and a file only the worktree has.
	writeFile(t, dir, "committed.txt", "worktree-only-needle here\nsecond line\n")
	writeFile(t, dir, "fresh.txt", "fresh-needle here\n")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func head(t *testing.T, r *Repo) string {
	t.Helper()
	h, err := r.Resolve("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func search(t *testing.T, r *Repo, query, scope string) *SearchResult {
	t.Helper()
	res, err := r.Search(context.Background(), head(t, r), true, query, scope, SearchLimit)
	if err != nil {
		t.Fatalf("Search(%q): %v", query, err)
	}
	return res
}

func matchTexts(res *SearchResult) []string {
	var out []string
	for _, g := range res.Groups {
		for _, m := range g.Matches {
			out = append(out, g.Path+":"+m.Text)
		}
	}
	return out
}

// TestSearchReadsCommittedBytesForTrackedFiles is the semantic the whole design
// turns on. The blob page renders a modified file's committed bytes, so a search
// that read the worktree copy would hand back line numbers that do not match the
// page its results link to.
func TestSearchReadsCommittedBytesForTrackedFiles(t *testing.T) {
	r, _ := searchFixture(t)

	if got := matchTexts(search(t, r, "committed-needle", "")); len(got) != 1 || !strings.HasPrefix(got[0], "committed.txt:committed-needle") {
		t.Errorf("committed content not found as committed: %v", got)
	}
	if got := matchTexts(search(t, r, "worktree-only-needle", "")); len(got) != 0 {
		t.Errorf("search read the worktree copy of a tracked file: %v", got)
	}
}

// TestSearchFindsFilesThatExistOnlyInTheWorktree: those the blob page renders
// from the filesystem must be findable, and marked as not committed.
func TestSearchFindsFilesThatExistOnlyInTheWorktree(t *testing.T) {
	r, _ := searchFixture(t)

	res := search(t, r, "fresh-needle", "")
	if res.Count != 1 {
		t.Fatalf("matches = %d, want 1: %v", res.Count, matchTexts(res))
	}
	g := res.Groups[0]
	if g.Path != "fresh.txt" || g.Change != Untracked {
		t.Errorf("worktree-only match not marked: path=%q change=%q", g.Path, g.Change)
	}
}

// TestSearchWithoutOverlaySeesOnlyTheTree: on a ref the worktree is not on,
// there is no overlay to mirror, so only committed content is searched.
func TestSearchWithoutOverlaySeesOnlyTheTree(t *testing.T) {
	r, _ := searchFixture(t)

	res, err := r.Search(context.Background(), head(t, r), false, "fresh-needle", "", SearchLimit)
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 0 {
		t.Errorf("overlay off still found worktree content: %v", matchTexts(res))
	}
}

func TestSearchNeverMatchesIgnoredOrBinaryFiles(t *testing.T) {
	r, _ := searchFixture(t)

	for _, query := range []string{"ignored-needle", "needle"} {
		for _, g := range search(t, r, query, "").Groups {
			if g.Path == "ignored.txt" {
				t.Errorf("%q matched an ignored file", query)
			}
			if g.Path == "binary.dat" {
				t.Errorf("%q matched a binary file", query)
			}
		}
	}
}

// TestSearchParsesHostilePathsAndText: a path may contain colons and digits, and
// matched text may look like a file:line prefix. Neither may be mistaken for the
// separator — which is why the records are NUL-delimited.
func TestSearchParsesHostilePathsAndText(t *testing.T) {
	r, _ := searchFixture(t)

	res := search(t, r, "colon-needle", "")
	if supportsUnusualFilenames() {
		if res.Count != 1 || res.Groups[0].Path != "weird:name.txt" {
			t.Errorf("a path containing a colon was misparsed: %+v", res.Groups)
		}
		if got := res.Groups[0].Matches[0].Line; got != 1 {
			t.Errorf("line = %d, want 1", got)
		}
	} else if res.Count != 0 {
		t.Errorf("a colon path was found on a filesystem that cannot hold one: %+v", res.Groups)
	}

	res = search(t, r, "needle-shaped", "")
	if res.Count != 1 || res.Groups[0].Path != "tricky.txt" {
		t.Fatalf("a text line that looks like a record was misparsed: %+v", res.Groups)
	}
	if got := res.Groups[0].Matches[0].Text; got != "42:needle-shaped text" {
		t.Errorf("text = %q, want the whole line", got)
	}
	if got := res.Groups[0].Matches[0].Line; got != 1 {
		t.Errorf("line = %d, want 1", got)
	}

	res = search(t, r, "spaced-needle", "")
	if res.Count != 1 || res.Groups[0].Path != "sub/spaced.txt" {
		t.Errorf("a path containing a space was not found: %+v", res.Groups)
	}
}

func TestSearchScopePrunes(t *testing.T) {
	r, _ := searchFixture(t)

	if got := search(t, r, "spaced-needle", "internal"); got.Count != 0 {
		t.Errorf("a scope that does not contain the match returned %v", matchTexts(got))
	}
	res := search(t, r, "spaced-needle", "sub")
	if res.Count != 1 || res.Groups[0].Path != "sub/spaced.txt" {
		t.Errorf("scoped search missed its own scope: %v", matchTexts(res))
	}
	// The worktree-only pass honours the scope too.
	if res := search(t, r, "fresh-needle", "sub"); res.Count != 0 {
		t.Errorf("worktree-only match ignored the scope: %v", matchTexts(res))
	}
}

// TestSearchTreatsTheQueryAsALiteral: the query is data, never a pattern and
// never a shell word.
func TestSearchTreatsTheQueryAsALiteral(t *testing.T) {
	r, _ := searchFixture(t)

	if got := search(t, r, "needle.*here", ""); got.Count != 0 {
		t.Errorf("a pattern-shaped query matched as a pattern: %v", matchTexts(got))
	}
	for _, hostile := range []string{`"; rm -rf ~`, "`echo pwned`", "$(echo pwned)", "needle|here"} {
		if got := search(t, r, hostile, ""); got.Count != 0 {
			t.Errorf("hostile query %q matched: %v", hostile, matchTexts(got))
		}
	}
	// And the literal itself is found.
	if got := search(t, r, "42:needle-shaped text", ""); got.Count != 1 {
		t.Errorf("literal query not found: %v", matchTexts(got))
	}
}

func TestSearchEmptyQueryFindsNothing(t *testing.T) {
	r, _ := searchFixture(t)

	for _, q := range []string{"", "   ", "\n"} {
		res := search(t, r, q, "")
		if res.Count != 0 || len(res.Groups) != 0 {
			t.Errorf("query %q returned %d matches", q, res.Count)
		}
	}
}

// TestSearchCapStopsEarly: the limit is a cost bound as well as a display bound,
// so it must be reported rather than silently applied.
func TestSearchCapStopsEarly(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("needle here\n")
	}
	writeFile(t, dir, "many.txt", b.String())
	run("add", "-A")
	run("commit", "-q", "-m", "many")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Search(context.Background(), head(t, r), true, "needle", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 10 {
		t.Errorf("count = %d, want the limit of 10", res.Count)
	}
	if !res.Capped {
		t.Error("the result does not admit it was capped")
	}
	if res.Stopped {
		t.Error("a cap is not a stop")
	}
}

// TestSearchDeadlineReportsPartial: a search cut short must say so rather than
// returning an error or pretending the prefix is the whole answer.
func TestSearchDeadlineReportsPartial(t *testing.T) {
	r, _ := searchFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := r.Search(ctx, head(t, r), true, "needle", "", SearchLimit)
	if err != nil {
		t.Fatalf("a cancelled search returned an error: %v", err)
	}
	if !res.Stopped {
		t.Error("a cut-short search does not admit it was stopped")
	}
}

// TestGrepStreamDeadlineMidStream puts the cancellation in the middle of the
// stream rather than before it: the reader has to stop when the deadline
// arrives, not wait for output that is no longer coming.
func TestGrepStreamDeadlineMidStream(t *testing.T) {
	r, _ := searchFixture(t)
	sha := head(t, r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := 0
	_, err := r.grepStream(ctx, []string{"grep", "-z", "-n", "-I", "-F", "-e", "needle", sha}, sha+":", func(string, int, string) bool {
		got++
		cancel() // as a deadline does: mid-stream, with matches already read
		return true
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got == 0 {
		t.Error("no records were read before the cancellation")
	}
}

func TestSearchUnknownRefIsAnError(t *testing.T) {
	r, _ := searchFixture(t)

	if _, err := r.Search(context.Background(), "no-such-ref", true, "needle", "", SearchLimit); err == nil {
		t.Error("an unknown ref did not produce an error")
	}
}

// TestGrepReaderReadsFieldsNotLines is the reason the reader exists in this
// shape. Records are "path NUL line NUL text newline", and only the NUL
// boundaries are trustworthy: a path may contain a newline (the old line-based
// reader produced a phantom result pointing at a file that did not exist), and a
// field may be longer than any buffer worth allocating (one 5 MB minified line
// used to fail the whole search).
func TestGrepReaderReadsFieldsNotLines(t *testing.T) {
	stream := "weird\nname.txt\x0012\x00text here\nplain.txt\x003\x00more text\n"
	g := &grepReader{rd: bufio.NewReader(strings.NewReader(stream))}

	read := func() (string, int, string, bool) {
		path, pathOK, err := g.field(0)
		if err != nil {
			t.Fatalf("path field: %v", err)
		}
		num, numOK, err := g.field(0)
		if err != nil {
			t.Fatalf("line field: %v", err)
		}
		text, textOK, err := g.field('\n')
		if err != nil {
			t.Fatalf("text field: %v", err)
		}
		n, convErr := strconv.Atoi(string(num))
		if convErr != nil {
			t.Fatalf("line field %q is not a number", num)
		}
		return string(path), n, string(text), pathOK && numOK && textOK
	}

	path, line, text, ok := read()
	if !ok || path != "weird\nname.txt" || line != 12 || text != "text here" {
		t.Errorf("first record read as %q %d %q (ok=%v)", path, line, text, ok)
	}
	path, line, text, ok = read()
	if !ok || path != "plain.txt" || line != 3 || text != "more text" {
		t.Errorf("second record read as %q %d %q (ok=%v)", path, line, text, ok)
	}
	if _, _, err := g.field(0); err != io.EOF {
		t.Errorf("end of stream = %v, want io.EOF", err)
	}
}

// TestGrepReaderSkipsAnOverlongField: a field too long for the buffer is dropped
// whole and the stream resynchronises, so one enormous generated line costs its
// own record and nothing else.
func TestGrepReaderSkipsAnOverlongField(t *testing.T) {
	huge := strings.Repeat("x", grepFieldSize+1024)
	stream := "a.txt\x001\x00" + huge + "\nb.txt\x002\x00fine\n"
	g := &grepReader{rd: bufio.NewReaderSize(strings.NewReader(stream), grepFieldSize)}

	path, _, err := g.field(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.field(0); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := g.field('\n'); ok || err != nil {
		t.Errorf("overlong text field: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	path, _, err = g.field(0)
	if err != nil {
		t.Fatalf("stream did not resynchronise: %v", err)
	}
	if string(path) != "b.txt" {
		t.Errorf("next record = %q, want b.txt", path)
	}
}

// TestSearchSurvivesAPathWithANewline: the same case end to end, because a path
// containing a newline used to be read as two ordinary records — one of them a
// phantom file, linking to a page that does not exist.
func TestSearchSurvivesAPathWithANewline(t *testing.T) {
	if !supportsUnusualFilenames() {
		t.Skip("Windows does not allow newlines in filenames")
	}
	dir := t.TempDir()
	run := func(args ...string) { gitOut(t, dir, args...) }
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	name := "weird\nname.txt"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "one")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Search(context.Background(), head(t, r), true, "needle", "", SearchLimit)
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 1 {
		t.Fatalf("matches = %d, want 1: %+v", res.Count, res.Groups)
	}
	if got := res.Groups[0].Path; got != name {
		t.Errorf("path = %q, want %q", got, name)
	}
}

// TestSearchRespectsTheRepoRoot: the grep must run inside the repository, not
// wherever the process happens to be.
func TestSearchRespectsTheRepoRoot(t *testing.T) {
	r, dir := searchFixture(t)

	// A file outside the repository that happens to contain the query must not
	// be reachable, and the search must still work from another directory.
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	res := search(t, r, "outside-needle", "")
	if res.Count != 0 {
		t.Errorf("search escaped the repository: %v", matchTexts(res))
	}
}
