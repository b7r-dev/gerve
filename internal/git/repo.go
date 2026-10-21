// Package git is gerve's only window into a repository. go-git provides
// object access (refs, trees, blobs); the git CLI covers what it does best:
// rename-aware patches, blame, worktree status, and log --follow.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
func (r *Repo) Resolve(name string) (string, error) {
	if name == "" {
		return "", ErrNotFound
	}
	h, err := r.r.ResolveRevision(plumbing.Revision(name))
	if err != nil {
		return "", ErrNotFound
	}
	return h.String(), nil
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
	h, err := r.Resolve(rev)
	if err != nil {
		return nil, err
	}
	c, err := r.r.CommitObject(plumbing.NewHash(h))
	if err != nil {
		return nil, ErrNotFound
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
	return r.gitStdin(ctx, nil, args...)
}

// gitStdin is git with a stdin payload. check-ignore takes its paths that way,
// NUL-terminated, which is what keeps a name containing a newline from being
// read as two paths.
func (r *Repo) gitStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	out, err := r.runGit(ctx, args, nil, stdin)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "dubious ownership") {
		// Repo owned by another uid: retry with an override config. A
		// safe.directory set via -c is not trusted by git; GIT_CONFIG_GLOBAL is.
		if out2, err2 := r.runGit(ctx, args, safeDirEnv(), stdin); err2 == nil {
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
	full := append([]string{"-c", "core.quotePath=false", "-c", "color.ui=never"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, &gitError{cmd: args[0], msg: strings.TrimSpace(errb.String()), err: err}
	}
	return out.Bytes(), nil
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
