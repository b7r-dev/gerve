// Package git is gerve's only window into a repository. go-git provides
// object access (refs, trees, blobs); the git CLI covers what it does best:
// rename-aware patches, blame, worktree status, and log --follow.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

var (
	// ErrNotRepo means the path is not inside a git repository.
	ErrNotRepo = errors.New("not a git repository")
	// ErrNotFound means a path or revision does not exist.
	ErrNotFound = errors.New("not found in repository")
	// ErrBadPath means a path tried to escape or touch something forbidden.
	ErrBadPath = errors.New("invalid repository path")
)

// CommitInfo is the lightweight commit identity used across listings.
type CommitInfo struct {
	Hash    string
	Short   string
	Author  string
	Time    time.Time
	Subject string
}

// RefInfo is one branch or tag.
type RefInfo struct {
	Name string // short name, e.g. "main", "feature/x", "v1.2"
	Kind string // "branch" | "tag"
	Hash string
}

// HeadInfo describes the state of HEAD.
type HeadInfo struct {
	Branch   string // empty when detached
	Detached bool
	Hash     string
}

// Repo is an opened, read-only view of a repository.
type Repo struct {
	root     string
	name     string
	r        *git.Repository
	worktree bool

	status *statusCache
	dl     *dirLogCache
}

// Open opens the repository at path (or any child of it). It never writes.
func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrNotRepo, path)
	}
	r, err := git.PlainOpenWithOptions(abs, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) && looksLikeBareGitDir(abs) {
			// go-git's PlainOpen walks up looking for a .git entry and never
			// considers the directory itself; open bare gitdirs explicitly.
			return openBare(abs)
		}
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return nil, fmt.Errorf("%w: %s (no .git found)", ErrNotRepo, path)
		}
		return nil, err
	}
	wt := true
	if _, err := r.Worktree(); err != nil {
		wt = false
	}
	name := filepath.Base(abs)
	if !wt && strings.HasSuffix(name, ".git") {
		name = strings.TrimSuffix(name, ".git")
	}
	return &Repo{
		root:     abs,
		name:     name,
		r:        r,
		worktree: wt,
		status:   &statusCache{},
		dl:       newDirLogCache(),
	}, nil
}

// Root is the absolute path gerve was pointed at.
func (r *Repo) Root() string { return r.root }

// Name is the display name: the directory name, minus ".git" for bare repos.
func (r *Repo) Name() string { return r.name }

// HasWorktree reports whether a worktree is checked out (false for bare).
func (r *Repo) HasWorktree() bool { return r.worktree }

// Resolve maps any revision spec (branch, tag, HEAD, short hash) to a full
// object hash. Annotated tags are peeled to their commit.
//
// go-git answers first — it is the fast path and needs no subprocess — with the
// git CLI behind it, because go-git cannot read every object git can: a shared
// clone keeps its objects in objects/info/alternates, which go-git does not
// follow, so a hash the log just printed can be unresolvable here while git is
// perfectly happy. Without the fallback that mismatch is a 404 on a commit the
// page itself linked to.
func (r *Repo) Resolve(name string) (string, error) {
	return r.ResolveCtx(context.Background(), name)
}

// ResolveCtx is Resolve under the caller's deadline: a page with a time budget
// must not spend it in a subprocess that was started without one.
func (r *Repo) ResolveCtx(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", ErrNotFound
	}
	if h, err := r.r.ResolveRevision(plumbing.Revision(name)); err == nil {
		return h.String(), nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// --end-of-options keeps a revision that begins with "-" from becoming a
	// flag; ^{commit} peels annotated tags and refuses anything that is not a
	// commit. One argv element, never a shell.
	out, err := r.git(cctx, "rev-parse", "--verify", "--end-of-options", name+"^{commit}")
	if err != nil {
		return "", ErrNotFound
	}
	h := strings.TrimSpace(string(out))
	if !isFullHex(h) {
		return "", ErrNotFound
	}
	return h, nil
}

// ExistsAt reports whether p names a file or directory at refHash. It asks git
// rather than go-git so a repository whose objects go-git cannot read still
// answers — and so a path that exists only on one side of a comparison can still
// scope it.
func (r *Repo) ExistsAt(ctx context.Context, refHash, p string) bool {
	if p == "" {
		return true
	}
	_, err := r.git(ctx, "cat-file", "-e", refHash+":"+p)
	return err == nil
}

// Head reports HEAD's branch (or detached state).
func (r *Repo) Head() HeadInfo {
	ref, err := r.r.Reference(plumbing.HEAD, false)
	if err != nil {
		return HeadInfo{}
	}
	if ref.Type() == plumbing.SymbolicReference && ref.Target().IsBranch() {
		return HeadInfo{Branch: ref.Target().Short()}
	}
	return HeadInfo{Detached: true, Hash: ref.Hash().String()}
}

// HeadHash resolves HEAD to a commit hash.
func (r *Repo) HeadHash() (string, error) {
	ref, err := r.r.Head()
	if err != nil {
		return "", err
	}
	return ref.Hash().String(), nil
}

// OverlayActive reports whether the live worktree should be merged into
// views of this hash: only when a worktree exists and the hash is HEAD's.
func (r *Repo) OverlayActive(refHash string) bool {
	if !r.worktree {
		return false
	}
	hh, err := r.HeadHash()
	return err == nil && hh == refHash
}

// Refs lists local branches and tags, branches first, both alphabetical.
func (r *Repo) Refs() ([]RefInfo, error) {
	iter, err := r.r.References()
	if err != nil {
		return nil, err
	}
	var out []RefInfo
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		switch {
		case ref.Name().IsBranch():
			out = append(out, RefInfo{Name: ref.Name().Short(), Kind: "branch", Hash: ref.Hash().String()})
		case ref.Name().IsTag():
			out = append(out, RefInfo{Name: ref.Name().Short(), Kind: "tag", Hash: ref.Hash().String()})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "branch"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// RecommendedRef picks a ref to open when the requested default (usually
// HEAD) does not resolve — e.g. HEAD points at a deleted branch. The most
// recently committed ref wins.
func (r *Repo) RecommendedRef() (string, error) {
	refs, err := r.Refs()
	if err != nil || len(refs) == 0 {
		return "", err
	}
	best, bestAt := "", time.Time{}
	for _, ref := range refs {
		c, err := r.r.CommitObject(plumbing.NewHash(ref.Hash))
		if err != nil {
			continue
		}
		at := c.Committer.When
		if at.IsZero() {
			at = c.Author.When
		}
		if at.After(bestAt) {
			best, bestAt = ref.Name, at
		}
	}
	return best, nil
}

// CommitMeta is a full commit for the commit page.
type CommitMeta struct {
	CommitInfo
	Body    string
	Parents []string
}

// Commit resolves a revision and returns its metadata. Annotated tags peel
// to commits; refs without commits fail with ErrNotFound.
func (r *Repo) Commit(rev string) (*CommitMeta, error) {
	return r.CommitCtx(context.Background(), rev)
}

// CommitCtx is Commit under the caller's deadline. Resolving and reading a
// commit may each spawn git — a comparison reads up to three of them inside one
// page budget — so the work has to be cancellable rather than run on Background.
func (r *Repo) CommitCtx(ctx context.Context, rev string) (*CommitMeta, error) {
	h, err := r.ResolveCtx(ctx, rev)
	if err != nil {
		return nil, err
	}
	c, err := r.r.CommitObject(plumbing.NewHash(h))
	if err != nil {
		return r.commitFromCLI(ctx, h)
	}
	m := &CommitMeta{Parents: make([]string, 0, len(c.ParentHashes))}
	for _, p := range c.ParentHashes {
		m.Parents = append(m.Parents, p.String())
	}
	m.Hash, m.Short = h, shortHash(h)
	m.Author = c.Author.Name
	m.Time = c.Committer.When
	if m.Time.IsZero() {
		m.Time = c.Author.When
	}
	msg := strings.TrimPrefix(c.Message, "\n")
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		m.Subject = strings.TrimSpace(msg[:i])
		m.Body = strings.TrimSpace(msg[i+1:])
	} else {
		m.Subject = strings.TrimSpace(msg)
	}
	return m, nil
}

// commitFromCLI builds CommitMeta with `git show` when go-git cannot read the
// object — the same reason Resolve has a fallback. The fields and the time
// choice (committer, falling back to author) match the go-git path, so a page
// does not change shape depending on which side answered.
func (r *Repo) commitFromCLI(ctx context.Context, hash string) (*CommitMeta, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// NUL, not a printable separator: the body is multi-line and may contain
	// anything printable. -z makes git terminate the record with NUL too.
	const format = "%H%x00%an%x00%ct%x00%P%x00%B"
	out, err := r.git(cctx, "show", "-s", "--no-color", "-z", "--format="+format, hash)
	if err != nil {
		return nil, ErrNotFound
	}
	f := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(f) < 5 {
		return nil, ErrNotFound
	}
	m := &CommitMeta{Parents: strings.Fields(f[3])}
	m.Hash, m.Short = f[0], shortHash(f[0])
	m.Author = f[1]
	if sec, err := strconv.ParseInt(f[2], 10, 64); err == nil {
		m.Time = time.Unix(sec, 0)
	}
	// Split the message the way the go-git path does: first line is the
	// subject, everything after it is the body. %s would fold a wrapped first
	// paragraph into one line and disagree with the same commit read by go-git.
	msg := strings.TrimPrefix(f[4], "\n")
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		m.Subject = strings.TrimSpace(msg[:i])
		m.Body = strings.TrimSpace(msg[i+1:])
	} else {
		m.Subject = strings.TrimSpace(msg)
	}
	return m, nil
}

// exitCode reports a git invocation's exit status when it has one. Some
// commands answer with the status rather than stdout: merge-base exits 1 for
// "no common ancestor", which is an answer, not a failure.
func exitCode(err error) (int, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}

// LastCommitFor returns the most recent commit touching a single path.
func (r *Repo) LastCommitFor(ctx context.Context, hash, path string) *CommitInfo {
	dir, base := splitDir(path)
	if base == "" {
		// the root: the ref's own tip is the answer
		if m, err := r.Commit(hash); err == nil {
			return &m.CommitInfo
		}
		return nil
	}
	lm := r.lastCommits(ctx, plumbing.NewHash(hash), dir, []string{base})
	if c, ok := lm[base]; ok {
		return &c
	}
	return nil
}

// --- git CLI plumbing -------------------------------------------------------

// recFmt is the shared record format for log-family commands: each commit
// starts with \x1e then H, h, author, unix time, subject joined by \x1f.
const recFmt = "%x1e%H%x1f%h%x1f%an%x1f%at%x1f%s"

func (r *Repo) git(ctx context.Context, args ...string) ([]byte, error) {
	return r.gitCall(ctx, nil, nil, args...)
}

// gitEnv is git with extra environment for one call — see noLazyFetch, which
// keeps a partial clone from fetching during a diff.
func (r *Repo) gitEnv(ctx context.Context, extraEnv []string, args ...string) ([]byte, error) {
	return r.gitCall(ctx, nil, extraEnv, args...)
}

// gitStdin is git with a stdin payload. check-ignore takes its paths that way,
// NUL-terminated, which is what keeps a name containing a newline from being
// read as two paths.
func (r *Repo) gitStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return r.gitCall(ctx, stdin, nil, args...)
}

// isDubiousOwnership reports the one git failure that a second invocation with
// an override config can fix: a repository owned by another uid.
func isDubiousOwnership(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "dubious ownership")
}

func (r *Repo) gitCall(ctx context.Context, stdin []byte, extraEnv []string, args ...string) ([]byte, error) {
	out, err := r.runGit(ctx, args, extraEnv, stdin)
	if isDubiousOwnership(err) {
		// Repo owned by another uid: retry with an override config. A
		// safe.directory set via -c is not trusted by git; GIT_CONFIG_GLOBAL is.
		env := append(append([]string{}, extraEnv...), safeDirEnv()...)
		if out2, err2 := r.runGit(ctx, args, env, stdin); err2 == nil {
			return out2, nil
		}
	}
	return out, err
}

// gitError is a failed git invocation that keeps the exit status reachable
// through errors.As. Some commands answer with it: check-ignore exits 1 to say
// "none of these paths match", which is an answer, not a failure.
type gitError struct {
	cmd string
	msg string
	err error
}

func (e *gitError) Error() string {
	if e.msg == "" {
		return "git " + e.cmd + ": " + e.err.Error()
	}
	return "git " + e.cmd + ": " + e.msg
}

func (e *gitError) Unwrap() error { return e.err }

func (r *Repo) runGit(ctx context.Context, args []string, extraEnv []string, stdin []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := r.runGitTo(ctx, args, extraEnv, stdin, &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// gitLimit is git with stdout capped. A command that would produce more fails
// with errOutputTooLarge instead of being buffered and measured afterwards —
// which is the only way a diff page can promise bounded memory rather than
// bounded output.
func (r *Repo) gitLimit(ctx context.Context, extraEnv []string, limit int, args ...string) ([]byte, error) {
	w := &capWriter{limit: limit}
	err := r.runGitTo(ctx, args, extraEnv, nil, w)
	if w.overflow {
		return nil, errOutputTooLarge
	}
	if isDubiousOwnership(err) {
		w = &capWriter{limit: limit}
		env := append(append([]string{}, extraEnv...), safeDirEnv()...)
		err = r.runGitTo(ctx, args, env, nil, w)
		if w.overflow {
			return nil, errOutputTooLarge
		}
	}
	if err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

var errOutputTooLarge = errors.New("git output exceeded the cap")

// capWriter stops accepting bytes at its limit and reports that as an error, so
// the command is abandoned rather than allowed to fill memory.
type capWriter struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		w.overflow = true
		return 0, errOutputTooLarge
	}
	return w.buf.Write(p)
}

// noLazyFetchKey is the variable that stops git from reaching for a promisor
// remote. It is set on every invocation, not just the diff-family calls: a page
// that reads a tree or a blob in a partial clone would otherwise fetch it, and
// gerve promises zero egress. Values passed in extraEnv are replaced for the
// same reason.
const noLazyFetchKey = "GIT_NO_LAZY_FETCH"

func (r *Repo) runGitTo(ctx context.Context, args []string, extraEnv []string, stdin []byte, stdout io.Writer) error {
	full := append([]string{"-c", "core.quotePath=false", "-c", "color.ui=never"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.root
	cmd.Env = withNoLazyFetch(append(os.Environ(), extraEnv...))
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &errb
	if err := cmd.Run(); err != nil {
		return &gitError{cmd: args[0], msg: strings.TrimSpace(errb.String()), err: err}
	}
	return nil
}

// withNoLazyFetch replaces any inherited value of the variable with a refusal.
// Inheriting "0" would let a read fetch, and the zero-egress promise does not
// depend on the caller's shell.
func withNoLazyFetch(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, noLazyFetchKey+"=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, noLazyFetchKey+"=1")
}

func safeDirEnv() []string {
	f, err := os.CreateTemp("", "gerve-gitconfig-*")
	if err != nil {
		return nil
	}
	fmt.Fprintf(f, "[safe]\n\tdirectory = *\n")
	f.Close()
	return []string{"GIT_CONFIG_GLOBAL=" + f.Name()}
}

// looksLikeBareGitDir reports whether path is itself a git directory
// (HEAD + objects + refs), i.e. a bare repository.
func looksLikeBareGitDir(path string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if fi, err := os.Stat(filepath.Join(path, name)); err != nil {
			return false
		} else if name == "HEAD" && fi.IsDir() {
			return false
		}
	}
	return true
}

func openBare(abs string) (*Repo, error) {
	storage := filesystem.NewStorage(osfs.New(abs), cache.NewObjectLRUDefault())
	r, err := git.Open(storage, nil)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(abs)
	name = strings.TrimSuffix(name, ".git")
	return &Repo{
		root:     abs,
		name:     name,
		r:        r,
		worktree: false,
		status:   &statusCache{},
		dl:       newDirLogCache(),
	}, nil
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func splitDir(p string) (dir, base string) {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}
