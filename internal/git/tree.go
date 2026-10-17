package git

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Entry is one row of a directory listing: a git tree entry, possibly
// overlaid with live worktree state.
type Entry struct {
	Name    string
	Path    string // slash-separated path from the repo root
	IsDir   bool
	Symlink bool
	Size    int64
	InGit   bool       // present in the committed tree
	Change  ChangeKind // "" when clean
	Last    *CommitInfo
}

// ListDir lists one directory at the given commit, merging live worktree
// state when the hash is HEAD's and a worktree exists. Directories that
// exist only in the worktree (fully untracked) are listed from the
// filesystem with every entry marked untracked.
func (r *Repo) ListDir(ctx context.Context, refHash, dir string) ([]Entry, error) {
	entries, gerr := r.gitEntries(refHash, dir)
	overlay := r.OverlayActive(refHash)
	if gerr != nil {
		if overlay && errors.Is(gerr, ErrNotFound) {
			ents, ferr := r.fsEntries(dir)
			if ferr != nil {
				return nil, gerr
			}
			return ents, nil
		}
		return nil, gerr
	}
	if overlay {
		entries = r.applyOverlay(ctx, entries, dir)
	}
	sortEntries(entries)

	var names []string
	for _, e := range entries {
		if e.InGit {
			names = append(names, e.Name)
		}
	}
	if len(names) > 0 {
		lm := r.lastCommits(ctx, plumbing.NewHash(refHash), dir, names)
		for i := range entries {
			if c, ok := lm[entries[i].Name]; ok {
				c := c
				entries[i].Last = &c
			}
		}
	}
	return entries, nil
}

func (r *Repo) gitEntries(refHash, dir string) ([]Entry, error) {
	commit, err := r.r.CommitObject(plumbing.NewHash(refHash))
	if err != nil {
		return nil, ErrNotFound
	}
	t, err := commit.Tree()
	if err != nil {
		return nil, ErrNotFound
	}
	if dir != "" {
		for _, seg := range strings.Split(dir, "/") {
			var found *struct {
				hash plumbing.Hash
				dir  bool
			}
			for _, e := range t.Entries {
				if e.Name == seg {
					found = &struct {
						hash plumbing.Hash
						dir  bool
					}{e.Hash, e.Mode == filemode.Dir}
					break
				}
			}
			if found == nil {
				return nil, ErrNotFound
			}
			if !found.dir {
				return nil, ErrNotFound // a file, not a directory
			}
			if t, err = r.r.TreeObject(found.hash); err != nil {
				return nil, ErrNotFound
			}
		}
	}
	entries := make([]Entry, 0, len(t.Entries))
	for _, e := range t.Entries {
		isDir := e.Mode == filemode.Dir
		var size int64
		if !isDir {
			if b, err := r.r.BlobObject(e.Hash); err == nil {
				size = b.Size
			}
		}
		entries = append(entries, Entry{
			Name:    e.Name,
			Path:    joinPath(dir, e.Name),
			IsDir:   isDir,
			Symlink: e.Mode == filemode.Symlink,
			Size:    size,
			InGit:   true,
		})
	}
	return entries, nil
}

// applyOverlay merges worktree status into a git listing. It returns the
// (possibly extended) slice.
func (r *Repo) applyOverlay(ctx context.Context, entries []Entry, dir string) []Entry {
	st := r.Status(ctx)
	if len(st) == 0 {
		return entries
	}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	idx := make(map[string]int, len(entries))
	for i, e := range entries {
		idx[e.Name] = i
	}
	added := map[string]bool{}
	for p, s := range st {
		if prefix != "" {
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			p = p[len(prefix):]
		}
		if p == "" {
			continue
		}
		if i := strings.IndexByte(p, '/'); i >= 0 {
			// The change lives inside a subdirectory. If that subdir is not
			// in git at all, surface it as an untracked directory.
			seg := p[:i]
			if _, inGit := idx[seg]; inGit || added[seg] {
				continue
			}
			if s.Kind == Untracked {
				entries = append(entries, Entry{
					Name: seg, Path: prefix + seg, IsDir: true, Change: Untracked,
				})
				added[seg] = true
			}
			continue
		}
		if gi, ok := idx[p]; ok {
			entries[gi].Change = s.Kind
			continue
		}
		if s.Kind.ContentFromWorktree() {
			entries = append(entries, Entry{
				Name: p, Path: prefix + p, Change: s.Kind,
			})
			added[p] = true
		}
	}
	return entries
}

// fsEntries lists a directory that exists only in the worktree.
func (r *Repo) fsEntries(dir string) ([]Entry, error) {
	abs, err := r.fsPath(dir)
	if err != nil {
		return nil, err
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, ErrNotFound
	}
	entries := make([]Entry, 0, len(dirents))
	for _, d := range dirents {
		if d.Name() == ".git" {
			continue
		}
		var size int64
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				size = info.Size()
			}
		}
		entries = append(entries, Entry{
			Name:   d.Name(),
			Path:   joinPath(dir, d.Name()),
			IsDir:  d.IsDir(),
			Size:   size,
			Change: Untracked,
		})
	}
	sortEntries(entries)
	return entries, nil
}

// fsPath maps a slash-separated repo-relative path to an absolute path,
// refusing escapes, .git, and symlinked exits from the repository.
func (r *Repo) fsPath(rel string) (string, error) {
	clean := path.Clean("/" + rel)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" {
		return r.root, nil
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".git" {
			return "", ErrBadPath
		}
	}
	p := filepath.Join(r.root, filepath.FromSlash(clean))
	rootReal, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		rootReal = r.root
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p, nil // missing; caller surfaces ErrNotFound
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(os.PathSeparator)) {
		return "", ErrBadPath
	}
	return p, nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// KindAt reports what lives at path on the given commit: "dir", "file",
// or "" when absent.
func (r *Repo) KindAt(refHash, p string) string {
	if p == "" {
		return "dir"
	}
	if err := validRepoPath(p); err != nil {
		return ""
	}
	commit, err := r.r.CommitObject(plumbing.NewHash(refHash))
	if err != nil {
		return ""
	}
	t, err := commit.Tree()
	if err != nil {
		return ""
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		var entry *object.TreeEntry
		for j := range t.Entries {
			if t.Entries[j].Name == seg {
				entry = &t.Entries[j]
				break
			}
		}
		if entry == nil {
			return ""
		}
		if i == len(segs)-1 {
			if entry.Mode == filemode.Dir {
				return "dir"
			}
			return "file"
		}
		if entry.Mode != filemode.Dir {
			return ""
		}
		if t, err = r.r.TreeObject(entry.Hash); err != nil {
			return ""
		}
	}
	return ""
}

// --- per-entry last commit --------------------------------------------------

type dirLogEntry struct {
	at time.Time
	v  map[string]CommitInfo
}

type dirLogCache struct {
	mu sync.Mutex
	m  map[string]dirLogEntry
}

func newDirLogCache() *dirLogCache {
	return &dirLogCache{m: map[string]dirLogEntry{}}
}

// lastCommits finds the newest commit touching each named child of dir,
// walking history via the CLI until every name is found (bounded).
func (r *Repo) lastCommits(ctx context.Context, hash plumbing.Hash, dir string, names []string) map[string]CommitInfo {
	key := hash.String() + "\x00" + dir
	r.dl.mu.Lock()
	if e, ok := r.dl.m[key]; ok && time.Since(e.at) < 3*time.Second {
		defer r.dl.mu.Unlock()
		out := make(map[string]CommitInfo, len(names))
		for _, n := range names {
			if c, ok := e.v[n]; ok {
				out[n] = c
			}
		}
		return out
	}
	r.dl.mu.Unlock()

	found := r.walkDirLog(ctx, hash, dir, names)

	r.dl.mu.Lock()
	if len(r.dl.m) > 512 {
		r.dl.m = map[string]dirLogEntry{} // crude eviction is fine locally
	}
	r.dl.m[key] = dirLogEntry{at: time.Now(), v: found}
	r.dl.mu.Unlock()
	return found
}

func (r *Repo) walkDirLog(ctx context.Context, hash plumbing.Hash, dir string, names []string) map[string]CommitInfo {
	args := []string{"log", "--no-color", "--max-count=400", "--format=" + recFmt, "--name-only", hash.String()}
	if dir != "" {
		args = append(args, "--", dir)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return map[string]CommitInfo{}
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	found := map[string]CommitInfo{}
	for _, rec := range recordChunks(out) {
		if len(rec) == 0 {
			continue
		}
		info, ok := parseCommitRecord(rec[0])
		if !ok {
			continue
		}
		for _, p := range rec[1:] {
			child := childName(p, dir)
			if want[child] && found[child].Hash == "" {
				found[child] = info
			}
		}
		if len(found) == len(want) {
			break
		}
	}
	return found
}

func childName(p, dir string) string {
	if dir != "" {
		if !strings.HasPrefix(p, dir+"/") {
			return ""
		}
		p = p[len(dir)+1:]
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

// recordChunks splits log-family output into per-commit line groups: each
// record begins at \x1e followed by the \x1f-joined commit fields, then
// optional file lines.
func recordChunks(out []byte) [][]string {
	var recs [][]string
	var cur []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "\x1e") {
			if cur != nil {
				recs = append(recs, cur)
			}
			cur = []string{strings.TrimPrefix(line, "\x1e")}
			continue
		}
		if cur == nil {
			continue
		}
		if line != "" {
			cur = append(cur, line)
		}
	}
	if cur != nil {
		recs = append(recs, cur)
	}
	return recs
}

func parseCommitRecord(rec string) (CommitInfo, bool) {
	f := strings.Split(rec, "\x1f")
	if len(f) < 5 {
		return CommitInfo{}, false
	}
	sec, err := parseInt64(f[3])
	if err != nil {
		return CommitInfo{}, false
	}
	return CommitInfo{
		Hash:    f[0],
		Short:   shortHash(f[0]),
		Author:  f[2],
		Time:    time.Unix(sec, 0),
		Subject: f[4],
	}, true
}

func parseInt64(s string) (int64, error) {
	var n int64
	if s == "" {
		return 0, errors.New("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
