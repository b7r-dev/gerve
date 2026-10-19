package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// SearchLimit caps how many matches one search collects. It is a display bound
// and a cost bound at once: git writes matches as it finds them, so stopping at
// the limit stops the walk — which is what makes searching a whole repository
// affordable rather than merely slower.
const SearchLimit = 200

// chunkPaths bounds how many worktree-only paths go into one grep invocation, so
// a repository with an enormous untracked tree cannot overflow the argument
// list. Several invocations cost more, but each one stays small.
const chunkPaths = 100

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
}

// SearchResult is a bounded set of matches, grouped by file.
type SearchResult struct {
	Count  int // total matches
	Groups []SearchFile
	// Capped reports that the limit was reached, and Stopped that the deadline
	// cut the search short. Either means the list is partial, and the page says
	// which: a truncated list that does not admit it is a lie by omission.
	Capped  bool
	Stopped bool
}

// Search finds lines matching query in refHash's tree, and — when overlay is set
// and the caller is on the ref the worktree stands on — in the files that exist
// only in the working tree.
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
// scope is a repo-relative path ("" for the whole repository). It is passed to
// git as a pathspec, which prunes the tree walk: a scoped search really does not
// read objects outside its scope.
func (r *Repo) Search(ctx context.Context, refHash string, overlay bool, query, scope string, limit int) (*SearchResult, error) {
	res := &SearchResult{}
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return res, nil
	}

	// -F: the query is a literal, never a pattern — a regex box in a search
	// field is a foot-gun, and an unbounded one at that. -z: NUL-separated
	// fields, so a path containing a colon or a digit cannot be mistaken for the
	// line-number separator (a file called "weird:name.txt" is ordinary). -I:
	// binary files are skipped.
	base := []string{"grep", "-z", "-n", "-I", "-F", "-e", query}

	var matches []SearchMatch
	add := func(path string, line int, text string, kind ChangeKind) bool {
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
	collect := func(path string, line int, text string) bool { return add(path, line, text, "") }
	if err := r.grepStream(ctx, args, refHash+":", collect); err != nil {
		if ctx.Err() == nil {
			return nil, err
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
				return add(path, line, text, kinds[path])
			}
			if err := r.grepStream(ctx, chunk, "", collectWorktree); err != nil {
				if ctx.Err() == nil {
					return nil, err
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
	res.Groups = groupMatches(matches)
	return res, nil
}

// groupMatches orders matches by file and then by line, and groups them.
func groupMatches(matches []SearchMatch) []SearchFile {
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
		groups = append(groups, SearchFile{Path: m.Path, Change: m.Change, Matches: []SearchMatch{m}})
	}
	return groups
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

// grepStream runs git grep, handing each record to fn until fn returns false or
// the output runs out, then stops the process. Reading while git writes is what
// makes the limit a limit: buffering the whole result set and truncating it
// afterwards would read every match in the repository first, which is precisely
// the cost the limit exists to avoid.
func (r *Repo) grepStream(ctx context.Context, args []string, prefix string, fn func(string, int, string) bool) error {
	full := append([]string{"-c", "core.quotePath=false", "-c", "color.ui=never"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return err
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // one long line is still one line
	for sc.Scan() {
		path, line, text, ok := grepRecord(sc.Bytes(), prefix)
		if !ok {
			continue
		}
		if !fn(path, line, text) {
			// The limit is reached: stop reading and stop git. Its exit status
			// is then not a verdict on the search, so it is not consulted.
			xerr := sc.Err()
			killErr := cmd.Process.Kill()
			waitErr := cmd.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if xerr != nil {
				return xerr
			}
			if killErr != nil && waitErr == nil {
				return killErr
			}
			return nil
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return ctx.Err() // the caller's deadline owns the outcome
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) && ee.ExitCode() == 1 {
			return nil // git grep exits 1 when nothing matched
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("git grep: %s", msg)
	}
	return nil
}

// grepRecord splits one `git grep -z -n` record — path NUL line NUL text — into
// its fields. The NUL separators are what make this safe to parse: a path may
// contain colons and digits ("weird:name.txt" is an ordinary filename) and so
// may the matched text, so splitting on ":" could not tell them apart. prefix,
// when non-empty, is the "<ref>:" git prints in front of paths when it is
// searching a tree rather than the worktree.
func grepRecord(rec []byte, prefix string) (path string, line int, text string, ok bool) {
	head, rest, found := bytes.Cut(rec, []byte{0})
	if !found {
		return "", 0, "", false
	}
	num, body, found := bytes.Cut(rest, []byte{0})
	if !found {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(string(num))
	if err != nil || n <= 0 {
		return "", 0, "", false
	}
	return strings.TrimPrefix(string(head), prefix), n, string(body), true
}
