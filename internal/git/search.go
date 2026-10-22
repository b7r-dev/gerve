package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// SearchLimit caps how many matches one search collects, and perFileLimit how
// many any one file contributes. The total is a display bound as much as a cost
// bound: emitted matches stop at it, which stops the walk too when matches are
// dense, though a sparse pattern still walks the tree to find its last one. The
// per-file cap is what keeps a single generated file from consuming the whole
// budget, because "where is this used?" deserves more than one file and a shrug.
const (
	SearchLimit   = 200
	perFileLimit  = 20
	chunkPaths    = 100
	grepFieldSize = 64 << 10
)

// SearchMatch is one matching line.
type SearchMatch struct {
	Path   string
	Line   int
	Text   string
	Change ChangeKind // how the line is not committed; empty when it is
}

// SearchFile is one file's matches, in order.
type SearchFile struct {
	Path    string
	Change  ChangeKind
	Matches []SearchMatch
	// More reports that this file has matches beyond the ones shown.
	More bool
}

// SearchResult is a bounded set of matches, grouped by file.
type SearchResult struct {
	Count  int // total matches
	Groups []SearchFile
	// Capped reports that the total limit was reached, and Stopped that the
	// deadline cut the search short. Skipped counts records that were too long
	// to read. Any of the three means the answer is partial, and the page says
	// which: a truncated list that does not admit it is a lie by omission.
	Capped  bool
	Stopped bool
	Skipped int

	// more marks files that had matches beyond those collected.
	more map[string]bool
}

// Search finds lines matching query in refHash's tree, and — when overlay is set
// and the caller is on the ref the worktree stands on — in the files that exist
// only in the working tree. Callers pass overlay from OverlayActive; this
// function does not check it, because only the server knows the request's ref.
//
// The split is the overlay's own rule, applied to search. A tracked file is
// searched as committed, because that is what the blob page renders for it; a
// modified file's worktree bytes are deliberately not searched, since a result
// pointing at line 40 of a file whose page shows a different line 40 is worse
// than no result at all. Files that exist only in the worktree (untracked,
// added, renamed) *are* searched, from the filesystem, because the blob page
// renders those from the filesystem too — and they are marked as such, so a
// match that is not committed is visibly not committed.
//
// scope is a repo-relative path ("" for the whole repository). On the tree pass
// it is a git pathspec, which prunes the walk so a scoped search really does not
// read objects outside its scope; on the worktree pass it is matched against
// each dirty path, because those paths are listed and filtered rather than
// pruned. Callers should pass a scope that names something real — a glob would
// mean two different things to the two passes.
func (r *Repo) Search(ctx context.Context, refHash string, overlay bool, query, scope string, limit int) (*SearchResult, error) {
	res := &SearchResult{}
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return res, nil
	}

	// -F: the query is a literal, never a pattern — a regex box in a search
	// field is a foot-gun, and an unbounded one at that. -z: NUL-separated
	// fields, so a path containing a colon, a digit, or even a newline cannot be
	// mistaken for a separator. -I: binary files are skipped.
	base := []string{"grep", "-z", "-n", "-I", "-F", "-e", query}

	var matches []SearchMatch
	perFile := map[string]int{}
	// emit returns false when the walk should stop.
	emit := func(path string, line int, text string, kind ChangeKind) bool {
		if perFile[path] >= perFileLimit {
			res.markMore(path)
			return true
		}
		perFile[path]++
		matches = append(matches, SearchMatch{Path: path, Line: line, Text: text, Change: kind})
		if len(matches) >= limit {
			res.Capped = true
			return false
		}
		return true
	}

	// 1. The ref's tree: committed bytes, so a line number here is the line
	// number the blob page will show.
	stopped := false
	args := append(append([]string{}, base...), refHash)
	if scope != "" {
		args = append(args, "--", scope)
	}
	collect := func(path string, line int, text string) bool { return emit(path, line, text, "") }
	skippedRecords, err := r.grepStream(ctx, args, refHash+":", collect)
	res.Skipped += skippedRecords
	if err != nil {
		if ctx.Err() == nil {
			return nil, r.statErr(err)
		}
		stopped = true // the deadline, not a failure: report what was found
	}

	// 2. The files that exist only in the worktree. No ref prefix in this mode,
	// and an explicit path list rather than a bare scope: with a pathspec alone,
	// --no-index would also search ignored files and the worktree copies of
	// tracked ones — the disagreement this design exists to avoid.
	if !stopped && overlay && len(matches) < limit {
		paths, kinds := r.worktreeOnly(ctx, scope)
		for i := 0; i < len(paths) && len(matches) < limit; i += chunkPaths {
			end := i + chunkPaths
			if end > len(paths) {
				end = len(paths)
			}
			chunk := append(append([]string{}, base...), "--no-index", "--")
			chunk = append(chunk, paths[i:end]...)
			collectWorktree := func(path string, line int, text string) bool {
				return emit(path, line, text, kinds[path])
			}
			n, err := r.grepStream(ctx, chunk, "", collectWorktree)
			res.Skipped += n
			if err != nil {
				if ctx.Err() == nil {
					return nil, r.statErr(err)
				}
				stopped = true
				break
			}
		}
	}

	// Either kind of cut-off means the list is partial, and the page says which:
	// a partial answer that admits it is more use than an error, and more honest
	// than a silent prefix.
	res.Stopped = stopped || ctx.Err() != nil
	res.Count = len(matches)
	res.Groups = groupMatches(matches, res.more)
	return res, nil
}

// markMore records that a file has matches beyond those collected.
func (r *SearchResult) markMore(path string) {
	if r.more == nil {
		r.more = map[string]bool{}
	}
	r.more[path] = true
}

// PathExists reports whether p names something on this ref or in the worktree —
// which is exactly the set of paths a search scope may name, because search
// covers both. A scope that names nothing would mean one thing to the tree pass
// (a pathspec, so a glob works) and another to the worktree pass (a literal
// prefix), so the server drops it rather than let the two disagree.
func (r *Repo) PathExists(ctx context.Context, refHash, p string) bool {
	if p == "" {
		return true
	}
	if r.KindAt(refHash, p) != "" {
		return true
	}
	for sp := range r.Status(ctx) {
		if sp == p || strings.HasPrefix(sp, p+"/") {
			return true
		}
	}
	return false
}

// worktreeOnly lists the paths that exist only in the working tree — the ones
// whose content gerve serves from the filesystem — within scope, with their
// change kind. Sorted, so chunking and output stay deterministic.
func (r *Repo) worktreeOnly(ctx context.Context, scope string) ([]string, map[string]ChangeKind) {
	status := r.Status(ctx)
	if len(status) == 0 {
		return nil, nil
	}
	kinds := map[string]ChangeKind{}
	var paths []string
	for p, st := range status {
		if !st.Kind.ContentFromWorktree() {
			continue
		}
		if scope != "" && p != scope && !strings.HasPrefix(p, scope+"/") {
			continue
		}
		paths = append(paths, p)
		kinds[p] = st.Kind
	}
	sort.Strings(paths)
	return paths, kinds
}

// groupMatches orders matches by file and then by line, and groups them.
func groupMatches(matches []SearchMatch, more map[string]bool) []SearchFile {
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	var groups []SearchFile
	for _, m := range matches {
		if n := len(groups); n > 0 && groups[n-1].Path == m.Path {
			groups[n-1].Matches = append(groups[n-1].Matches, m)
			continue
		}
		groups = append(groups, SearchFile{
			Path:    m.Path,
			Change:  m.Change,
			More:    more[m.Path],
			Matches: []SearchMatch{m},
		})
	}
	return groups
}

// grepStream runs git grep, handing each record to fn until fn returns false or
// the output runs out. Reading while git writes is what makes the limit a limit:
// buffering a repository's worth of matches to throw most of them away is
// exactly the cost the limit exists to avoid.
//
// A repository whose owner differs from the running uid needs the same retry
// every other git call in this package gets, so a search works wherever tree,
// blob, and log work. It is only safe to retry while nothing has been emitted —
// otherwise the retry would duplicate what the first attempt found.
func (r *Repo) grepStream(ctx context.Context, args []string, prefix string, fn func(string, int, string) bool) (int, error) {
	emitted := 0
	counted := func(path string, line int, text string) bool {
		emitted++
		return fn(path, line, text)
	}
	skipped, err := r.grepAttempt(ctx, args, prefix, counted, nil)
	if err != nil && emitted == 0 && strings.Contains(strings.ToLower(err.Error()), "dubious ownership") {
		return r.grepAttempt(ctx, args, prefix, fn, safeDirEnv())
	}
	return skipped, err
}

// grepAttempt is one git grep invocation.
func (r *Repo) grepAttempt(ctx context.Context, args []string, prefix string, fn func(string, int, string) bool, extraEnv []string) (int, error) {
	full := append([]string{"-c", "core.quotePath=false", "-c", "color.ui=never"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.root
	// grep streams its own stdout, so it cannot go through runGitTo — which
	// means the zero-egress guard is not inherited. It applies it here for the
	// same reason: a search in a partial clone must fail, not fetch.
	cmd.Env = withNoLazyFetch(append(os.Environ(), extraEnv...))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	g := &grepReader{rd: bufio.NewReaderSize(stdout, grepFieldSize)}
	reachedLimit := false
	var readErr error
	for {
		path, pathOK, err := g.field(0)
		if err != nil {
			readErr = err
			break
		}
		num, numOK, err := g.field(0)
		if err != nil {
			readErr = err
			break
		}
		text, textOK, err := g.field('\n')
		if err != nil {
			readErr = err
			break
		}
		if !pathOK || !numOK || !textOK {
			g.skipped++ // a record too long to read, and skipped whole
			continue
		}
		n, convErr := strconv.Atoi(string(num))
		if convErr != nil || n <= 0 {
			continue
		}
		// git prints the tree it was given in front of every path; it is our own
		// argument, so it is stripped by length rather than parsed off.
		name := string(path)
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
		}
		if !fn(name, n, string(text)) {
			reachedLimit = true
			break
		}
	}

	// Any exit other than the end of the output leaves git writing into a pipe
	// nobody is reading, so it is killed *before* Wait rather than after: Wait
	// closes the read end only once the process has exited, which is the
	// deadlock — a full pipe and a process waiting to be read.
	if reachedLimit || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()

	// A read failure is reported as itself, never as the deadline: they call for
	// different words on the page, and blaming a timeout for a line too long
	// would send the reader off narrowing a scope that is not the problem.
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return g.skipped, readErr
	}
	if reachedLimit {
		return g.skipped, nil
	}
	if ctx.Err() != nil {
		return g.skipped, ctx.Err() // the caller's deadline owns the outcome
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) && ee.ExitCode() == 1 {
			// git grep exits 1 when nothing matched — and also when it could
			// not read an object, which it explains on stderr. Reporting that
			// as "no matches" is the one answer a search must never invent.
			if msg := strings.TrimSpace(errb.String()); msg != "" {
				return g.skipped, fmt.Errorf("git grep: %s", msg)
			}
			return g.skipped, nil // nothing matched
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return g.skipped, fmt.Errorf("git grep: %s", msg)
	}
	return g.skipped, nil
}

// grepReader reads `git grep -z -n` output as fields: a NUL-terminated path, a
// NUL-terminated line number, then the matched text up to its newline. Only the
// NUL boundaries are trustworthy — a path may contain a newline, a colon, or a
// digit, and the text may contain the same — so the stream is consumed field by
// field rather than record by record, which is what keeps a strange path from
// being read as several ordinary ones.
type grepReader struct {
	rd      *bufio.Reader
	skipped int
}

// field reads one delimiter-terminated field, without the delimiter. ok is false
// when the field was longer than the buffer and has been discarded — one
// enormous generated line must not fail the search or desynchronise the parse.
// err is io.EOF when the output has ended.
func (g *grepReader) field(delim byte) (val []byte, ok bool, err error) {
	part, err := g.rd.ReadSlice(delim)
	if errors.Is(err, bufio.ErrBufferFull) {
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = g.rd.ReadSlice(delim)
		}
		return nil, false, err
	}
	if err != nil {
		return nil, false, err
	}
	return part[:len(part)-1], true, nil
}
