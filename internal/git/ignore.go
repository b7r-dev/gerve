package git

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// ignoredAmong reports which of the given repo-relative paths the repository's
// ignore rules cover.
//
// The answer comes from git rather than a matcher here, so nested .gitignore
// files, .git/info/exclude, and the user's global excludes apply exactly as
// they do to `git status`. Note what check-ignore actually answers: "does a
// pattern match this path", not "would status ignore it". It will name a
// *tracked* file that a later pattern happens to match. Callers must therefore
// only ask about paths that cannot be tracked, which is true by construction of
// the worktree-only listing this serves: a directory that is not in the
// committed tree has no tracked children.
func (r *Repo) ignoredAmong(ctx context.Context, paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var stdin bytes.Buffer
	for _, p := range paths {
		stdin.WriteString(p)
		stdin.WriteByte(0)
	}
	out, err := r.gitStdin(ctx, stdin.Bytes(), "check-ignore", "-z", "--stdin")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // no path matches — the common answer, not a failure
		}
		return nil, err
	}
	set := make(map[string]bool, len(paths))
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			set[string(p)] = true
		}
	}
	return set, nil
}
