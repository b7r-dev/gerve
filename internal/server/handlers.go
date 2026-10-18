package server

import (
	"bytes"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
)

// --- handlers ----------------------------------------------------------------

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ref := s.def
	if _, err := s.repo.Resolve(ref); err != nil {
		rec, rerr := s.repo.RecommendedRef()
		if rerr != nil || rec == "" {
			refs, _ := s.repo.Refs()
			if len(refs) == 0 {
				s.rnd.Page(w, "empty", render.EmptyPage{Base: s.newBase("", "", "tree")})
				return
			}
			// dangling HEAD with other refs: land on the ref list
			http.Redirect(w, r, "/refs", http.StatusFound)
			return
		}
		ref = rec
	}
	http.Redirect(w, r, render.URLFor("tree", ref, ""), http.StatusFound)
}

func (s *Server) tree(w http.ResponseWriter, r *http.Request) {
	ref, p, ok := s.splitRefPath(strings.TrimPrefix(r.URL.Path, "/tree/"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "not found", "That ref or path could not be resolved in this repository.")
		return
	}
	hash, err := s.repo.Resolve(ref)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown ref", "No branch, tag, or commit named "+ref+".")
		return
	}
	ctx := r.Context()
	entries, err := s.repo.ListDir(ctx, hash, p)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "path not found", "Nothing to list at "+p+" on "+ref+".")
		return
	}
	overlay := s.repo.OverlayActive(hash)

	page := render.TreePage{
		Base:    s.newBase(ref, p, "tree"),
		Entries: entries,
		DirLast: s.repo.LastCommitFor(ctx, hash, p),
		Overlay: overlay,
	}

	// README at this level, rendered from git or worktree per overlay state.
	for i := range entries {
		n := strings.ToLower(entries[i].Name)
		if entries[i].IsDir {
			continue
		}
		if n == "readme.md" || n == "readme.markdown" || n == "readme.txt" || n == "readme" {
			if bl, err := s.repo.Blob(ctx, hash, entries[i].Path, overlay); err == nil && len(bl.Content) <= 512<<10 {
				switch {
				case strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".markdown"):
					page.Readme = s.rnd.Markdown(bl.Content, s.linkRewriter(ref, hash, p))
				default:
					page.Readme = s.rnd.PlainText(bl.Content)
				}
				page.ReadmeName = entries[i].Name
			}
			break
		}
	}

	if p == "" {
		if lp, err := s.repo.Log(ctx, hash, 0, 6, "", false); err == nil {
			page.Recent = lp.Commits
		}
	}

	s.rnd.Page(w, "tree", page)
}

func (s *Server) blob(w http.ResponseWriter, r *http.Request) {
	ref, p, ok := s.splitRefPath(strings.TrimPrefix(r.URL.Path, "/blob/"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "not found", "That ref or path could not be resolved in this repository.")
		return
	}
	hash, err := s.repo.Resolve(ref)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown ref", "No branch, tag, or commit named "+ref+".")
		return
	}
	ctx := r.Context()
	overlay := s.repo.OverlayActive(hash)

	b, err := s.repo.Blob(ctx, hash, p, overlay)
	if err != nil {
		s.blobErr(w, r, err)
		return
	}

	page := render.BlobPage{Base: s.newBase(ref, p, "blob"), Blob: b, Last: s.repo.LastCommitFor(ctx, hash, p)}

	if overlay {
		if st, ok := s.repo.Status(ctx)[p]; ok {
			page.Change = st.Kind
		}
	}
	if b.Size > 1<<20 || b.Truncated {
		page.Large = true
	}
	if !b.Binary && len(b.Content) == 0 {
		page.Empty = true
	}
	if !b.Binary && b.Image == "" && !page.Large {
		page.Lines = bytes.Count(b.Content, []byte{'\n'})
		if len(b.Content) > 0 && b.Content[len(b.Content)-1] != '\n' {
			page.Lines++
		}
		source := func() string {
			if code, ok := s.rnd.Highlight(b.Content, filepath.Base(p)); ok {
				return string(code)
			}
			return string(s.rnd.PlainCode(b.Content))
		}
		lower := strings.ToLower(p)
		if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") {
			page.IsMD = true
			if r.URL.Query().Get("source") == "1" {
				// source view: highlighted markdown
				page.RenderURL = render.URLFor("blob", ref, p)
				page.Code = template.HTML(source())
			} else {
				// rendered view (default), links rewritten with this ref pinned
				page.SourceURL = render.URLFor("blob", ref, p) + "?source=1"
				page.Code = s.rnd.Markdown(b.Content, s.linkRewriter(ref, hash, parentDir(p)))
			}
		} else {
			page.Code = template.HTML(source())
		}
	}

	committedHere := !b.FromWorktree && page.Change != git.Untracked && page.Change != git.Added && page.Change != git.Renamed
	page.CanHistory = committedHere
	page.CanBlame = committedHere && !b.Binary && b.Size <= 4<<20

	s.rnd.Page(w, "blob", page)
}

func (s *Server) blobErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, git.ErrBadPath):
		s.errorPage(w, r, http.StatusBadRequest, "bad path", "That path is not allowed.")
	case errors.Is(err, git.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "file not found", "No file at that path on this ref.")
	default:
		s.errorPage(w, r, http.StatusInternalServerError, "error", "Something went wrong reading that file.")
	}
}

func (s *Server) blame(w http.ResponseWriter, r *http.Request) {
	ref, p, ok := s.splitRefPath(strings.TrimPrefix(r.URL.Path, "/blame/"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "not found", "That ref or path could not be resolved in this repository.")
		return
	}
	hash, err := s.repo.Resolve(ref)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown ref", "No branch, tag, or commit named "+ref+".")
		return
	}
	groups, err := s.repo.Blame(r.Context(), hash, p)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "no blame",
			"Blame is not available for this path here — it may be untracked, binary, or too large.")
		return
	}
	s.rnd.Page(w, "blame", render.BlamePage{Base: s.newBase(ref, p, "blame"), Groups: groups})
}

func (s *Server) raw(w http.ResponseWriter, r *http.Request) {
	ref, p, ok := s.splitRefPath(strings.TrimPrefix(r.URL.Path, "/raw/"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	hash, err := s.repo.Resolve(ref)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	b, err := s.repo.Blob(r.Context(), hash, p, s.repo.OverlayActive(hash))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", b.ContentType())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition",
			mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	}
	http.ServeContent(w, r, filepath.Base(p), time.Time{}, bytes.NewReader(b.Content))
}

func (s *Server) commits(w http.ResponseWriter, r *http.Request) {
	ref := strings.Trim(strings.TrimPrefix(r.URL.Path, "/commits"), "/")
	if ref == "" {
		ref = s.def
	}
	hash, err := s.repo.Resolve(ref)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown ref", "No branch, tag, or commit named "+ref+".")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 0 {
		page = 0
	}
	path, ok := validQueryParamPath(q.Get("path"))
	if !ok {
		path = ""
	}
	follow := path != "" && s.repo.IsFileAt(hash, path)

	const per = 50
	lp, err := s.repo.Log(r.Context(), hash, page, per, path, follow)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, "error", "Something went wrong walking history.")
		return
	}

	p := render.CommitsPage{Base: s.newBase(ref, path, "tree"), Log: lp, Page: page, Path: path, Follow: follow}
	mkURL := func(pg int) string {
		u := "/commits/" + url.PathEscape(ref) + "?page=" + strconv.Itoa(pg)
		if path != "" {
			u += "&path=" + url.QueryEscape(path)
		}
		return u
	}
	if page > 0 {
		p.PrevURL = mkURL(page - 1)
	}
	if lp.Next {
		p.NextURL = mkURL(page + 1)
	}
	s.rnd.Page(w, "commits", p)
}

func (s *Server) commit(w http.ResponseWriter, r *http.Request) {
	rev := strings.Trim(strings.TrimPrefix(r.URL.Path, "/commit"), "/")
	if rev == "" || strings.Contains(rev, "/") {
		s.errorPage(w, r, http.StatusNotFound, "not found", "A commit URL looks like /commit/{hash}.")
		return
	}
	meta, err := s.repo.Commit(rev)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "commit not found", "No commit named "+rev+" in this repository.")
		return
	}
	ctx := r.Context()

	var parent string
	merge := len(meta.Parents) > 1
	if merge {
		parent = meta.Parents[0]
	}
	diff, err := s.repo.CommitDiff(ctx, meta.Hash, parent)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, "error", "Something went wrong computing the diff.")
		return
	}

	view := r.URL.Query().Get("view")
	if view != "side" {
		view = "unified"
	}

	// Pin the page to the commit itself: the "view file" links, breadcrumbs,
	// and ref switcher all operate on the reviewed commit, not the default
	// ref — otherwise links silently show a different file version.
	page := render.CommitPage{Base: s.newBase(meta.Hash, "", "tree"), Meta: meta, View: view, Merge: merge}
	page.Files = make([]render.FileView, 0, len(diff.Files))
	for _, f := range diff.Files {
		fv := render.FileView{FileDiff: f}
		if !f.Binary && f.Patch != "" {
			fv.Rows = git.UnifiedRows(f.Patch)
			if view == "side" {
				fv.Sb = git.SideBySide(f.Patch)
			}
		}
		page.Adds += f.Adds
		page.Dels += f.Dels
		page.Files = append(page.Files, fv)
	}
	page.SideURL = "/commit/" + rev + "?view=side"
	page.UnifiedURL = "/commit/" + rev

	s.rnd.Page(w, "commit", page)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	ref, p, ok := s.splitRefPath(strings.TrimPrefix(r.URL.Path, "/history/"))
	if !ok {
		s.errorPage(w, r, http.StatusNotFound, "not found", "That ref or path could not be resolved in this repository.")
		return
	}
	http.Redirect(w, r,
		"/commits/"+url.PathEscape(ref)+"?path="+url.QueryEscape(p),
		http.StatusFound)
}

func (s *Server) refsPage(w http.ResponseWriter, r *http.Request) {
	refs, err := s.repo.Refs()
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, "error", "Could not list refs.")
		return
	}
	page := render.RefsPage{Base: s.newBase(s.def, "", "tree")}
	for _, ref := range refs {
		e := render.RefsEntry{RefInfo: ref}
		if m, err := s.repo.Commit(ref.Hash); err == nil {
			e.Info = m.CommitInfo
		}
		if ref.Kind == "branch" {
			page.Branches = append(page.Branches, e)
		} else {
			page.Tags = append(page.Tags, e)
		}
	}
	s.rnd.Page(w, "refs", page)
}

// serveStatic serves the embedded assets. A request whose ?v= matches the
// file's content hash is immutable and cached hard; anything else revalidates,
// so a stale stylesheet can never outlive a restart.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if v := r.URL.Query().Get("v"); v != "" && v == s.rnd.AssetVersion(name) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if name == "app.css" {
		if css := s.rnd.VersionedCSS(); css != nil {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			http.ServeContent(w, r, "app.css", time.Time{}, bytes.NewReader(css))
			return
		}
	}
	s.static.ServeHTTP(w, r)
}

// helpers ---------------------------------------------------------------------

// linkRewriter returns a markdown link rewriter that resolves repo-relative
// targets against the ref being browsed and pins that ref into the URL.
// Targets missing at that ref fall back to the default ref; targets missing
// everywhere keep the pin and 404 honestly.
func (s *Server) linkRewriter(refName, refHash, baseDir string) func(string) string {
	defHash, _ := s.repo.Resolve(s.def)
	return func(href string) string {
		clean, ok := repoRelative(baseDir, href)
		if !ok {
			return ""
		}
		kind := s.repo.KindAt(refHash, clean)
		urlRef := refName
		if kind == "" && defHash != "" && defHash != refHash {
			if k := s.repo.KindAt(defHash, clean); k != "" {
				kind, urlRef = k, s.def
			}
		}
		switch kind {
		case "dir":
			return render.URLFor("tree", urlRef, clean) + "/"
		case "file":
			if isRasterExt(clean) {
				return render.URLFor("raw", urlRef, clean)
			}
			return render.URLFor("blob", urlRef, clean)
		default:
			return render.URLFor("blob", refName, clean)
		}
	}
}

// repoRelative resolves a markdown link against the directory of the file
// containing it. A leading "/" is repo-root-relative; escapes and .git are
// rejected. Query strings are stripped (fragments are handled upstream).
func repoRelative(baseDir, href string) (string, bool) {
	h := href
	if i := strings.IndexByte(h, '?'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return "", false
	}
	if unescaped, err := url.PathUnescape(h); err == nil {
		h = unescaped
	}
	join := strings.TrimSuffix(baseDir, "/")
	if strings.HasPrefix(h, "/") {
		h = strings.TrimPrefix(h, "/")
		join = ""
	}
	clean := path.Clean(join + "/" + h)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." || clean == "" {
		return "", false
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." || seg == ".git" {
			return "", false
		}
	}
	return clean, true
}

func isRasterExt(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".avif":
		return true
	}
	return false
}

func parentDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func validQueryParamPath(p string) (string, bool) {
	if p == "" {
		return "", true
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".", "..", ".git":
			return "", false
		}
	}
	return strings.Trim(p, "/"), true
}
