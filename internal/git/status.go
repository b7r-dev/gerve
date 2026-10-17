package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ChangeKind is the worktree overlay state of a path relative to HEAD.
type ChangeKind string

const (
	// ChangeNone means clean (no overlay state).
	ChangeNone ChangeKind = ""
	Untracked  ChangeKind = "untracked"
	Added      ChangeKind = "added"
	Modified   ChangeKind = "modified"
	Deleted    ChangeKind = "deleted"
	Renamed    ChangeKind = "renamed"
)

// FileStatus is one path's worktree state.
type FileStatus struct {
	Path   string
	Kind   ChangeKind
	Staged bool
}

// ContentFromWorktree reports whether content for this state must come from
// the filesystem (the file exists only in the worktree/index, not in HEAD).
func (k ChangeKind) ContentFromWorktree() bool {
	return k == Untracked || k == Added || k == Renamed
}

// Status returns the worktree status of every changed path, computed by the
// git CLI and cached. On huge repos the first computation may time out; the
// empty map is returned and a background refresh populates the cache.
func (r *Repo) Status(ctx context.Context) map[string]FileStatus {
	if !r.worktree {
		return nil
	}
	r.status.mu.Lock()
	defer r.status.mu.Unlock()

	now := time.Now()
	idx := r.indexMtime()
	if r.status.val != nil && now.Sub(r.status.at) < 2*time.Second && r.status.idxMod.Equal(idx) {
		return r.status.val
	}

	// First-ever computation runs synchronously with a deadline; later
	// refreshes go to the background so pages never block on a big repo.
	if r.status.val == nil {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		m, err := r.computeStatus(cctx)
		r.status.at = now
		r.status.idxMod = idx
		if err == nil {
			r.status.val = m
			return m
		}
		go r.refreshStatus()
		return map[string]FileStatus{}
	}

	go r.refreshStatus()
	return r.status.val
}

func (r *Repo) refreshStatus() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m, err := r.computeStatus(ctx)
	if err != nil {
		return
	}
	r.status.mu.Lock()
	r.status.val, r.status.at, r.status.idxMod = m, time.Now(), r.indexMtime()
	r.status.mu.Unlock()
}

func (r *Repo) computeStatus(ctx context.Context) (map[string]FileStatus, error) {
	out, err := r.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseStatus(out), nil
}

func (r *Repo) indexMtime() time.Time {
	p := filepath.Join(r.root, ".git", "index")
	if fi, err := os.Stat(p); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

func parseStatus(out []byte) map[string]FileStatus {
	m := map[string]FileStatus{}
	toks := bytes.Split(out, []byte{0})
	for i := 0; i < len(toks)-1; i++ {
		t := toks[i]
		if len(t) < 4 {
			continue
		}
		x, y := t[0], t[1]
		p := string(t[3:])
		kind, staged := classifyStatus(x, y)
		if p == "" {
			continue
		}
		m[p] = FileStatus{Path: p, Kind: kind, Staged: staged}
		// Rename records in -z mode are followed by the original path.
		if (x == 'R' || y == 'R' || x == 'C' || y == 'C') && i+1 < len(toks)-1 {
			orig := string(toks[i+1])
			i++
			if orig != "" {
				m[orig] = FileStatus{Path: orig, Kind: Deleted, Staged: staged}
			}
		}
	}
	return m
}

func classifyStatus(x, y byte) (ChangeKind, bool) {
	staged := x != ' ' && x != '?'
	switch {
	case x == '?' && y == '?':
		return Untracked, false
	case x == 'D' || y == 'D':
		return Deleted, staged
	case x == 'R' || y == 'R' || x == 'C' || y == 'C':
		return Renamed, staged
	case x == 'A' || y == 'A':
		return Added, staged
	default:
		return Modified, staged
	}
}

type statusCache struct {
	mu     sync.Mutex
	val    map[string]FileStatus
	at     time.Time
	idxMod time.Time
}
