package git

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const maxBlob = 8 << 20 // refuse to hold more than 8 MiB in memory

// Blob is one file's content and metadata, from git objects or (for
// worktree-only files at HEAD) from the filesystem.
type Blob struct {
	Path         string
	Size         int64
	Truncated    bool
	Binary       bool
	Image        string // "png", "jpeg", … when previewable as an image
	FromWorktree bool
	Content      []byte
}

// Blob fetches a file at the given commit. With the overlay active, files
// that exist only in the worktree are read from disk.
func (r *Repo) Blob(ctx context.Context, refHash, path string, overlay bool) (*Blob, error) {
	if err := validRepoPath(path); err != nil {
		return nil, err
	}
	if overlay && r.worktree {
		if st, ok := r.Status(ctx)[path]; ok && st.Kind.ContentFromWorktree() {
			if b, err := r.fsBlob(path); err == nil {
				b.FromWorktree = true
				return b, nil
			}
		}
	}
	return r.gitBlob(refHash, path)
}

func (r *Repo) gitBlob(refHash, path string) (*Blob, error) {
	commit, err := r.r.CommitObject(plumbing.NewHash(refHash))
	if err != nil {
		return nil, ErrNotFound
	}
	t, err := commit.Tree()
	if err != nil {
		return nil, ErrNotFound
	}
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		var entry *object.TreeEntry
		for j := range t.Entries {
			if t.Entries[j].Name == seg {
				entry = &t.Entries[j]
				break
			}
		}
		if entry == nil {
			return nil, ErrNotFound
		}
		last := i == len(segs)-1
		if entry.Mode == filemode.Dir {
			if last {
				return nil, ErrNotFound // asked for a file, found a directory
			}
			if t, err = r.r.TreeObject(entry.Hash); err != nil {
				return nil, ErrNotFound
			}
			continue
		}
		if !last {
			return nil, ErrNotFound // walked through a file
		}
		b, err := r.r.BlobObject(entry.Hash)
		if err != nil {
			return nil, ErrNotFound
		}
		return readBlob(path, b.Size, b.Reader)
	}
	return nil, ErrNotFound
}

func (r *Repo) fsBlob(path string) (*Blob, error) {
	p, err := r.fsPath(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return nil, ErrNotFound
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, ErrNotFound
	}
	defer f.Close()
	return readBlob(path, fi.Size(), func() (io.ReadCloser, error) { return io.NopCloser(f), nil })
}

func readBlob(path string, size int64, open func() (io.ReadCloser, error)) (*Blob, error) {
	rd, err := open()
	if err != nil {
		return nil, ErrNotFound
	}
	defer rd.Close()
	content := make([]byte, maxBlob+1)
	n, err := io.ReadFull(rd, content)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	content = content[:n]
	b := &Blob{Path: path, Size: size, Content: content}
	b.Truncated = size > int64(len(content))
	b.Binary, b.Image = sniffBlob(content)
	return b, nil
}

func sniffBlob(content []byte) (binary bool, image string) {
	head := content
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) < 0 {
		return false, ""
	}
	ct := http.DetectContentType(head)
	if strings.HasPrefix(ct, "image/") {
		sub := strings.TrimPrefix(ct, "image/")
		switch sub {
		case "png", "jpeg", "gif", "webp", "bmp", "avif":
			return true, sub
		}
	}
	return true, ""
}

// ContentType for raw serving. Everything textual is served as plain text —
// including html and svg — so raw URLs can never execute in the browser.
func (b *Blob) ContentType() string {
	if b.Image != "" {
		return "image/" + b.Image
	}
	if b.Binary {
		return "application/octet-stream"
	}
	return "text/plain; charset=utf-8"
}

func validRepoPath(p string) error {
	if p == "" {
		return ErrBadPath
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".", "..", ".git":
			return ErrBadPath
		}
	}
	return nil
}
