package server

import (
	"bytes"
	"context"
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
				s.page(w, r, http.StatusOK, "empty", render.EmptyPage{Base: s.newBase("", "", "tree")})
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
	listing, err := s.repo.ListDir(ctx, hash, p)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "path not found", "Nothing to list at "+p+" on "+ref+".")
		return
	}
	overlay := s.repo.OverlayActive(hash)

	page := render.TreePage{
		Base:    s.newBase(ref, p, "tree"),
		Entries: listing.Entries,
		Ignored: listing.Ignored,
		DirLast: s.repo.LastCommitFor(ctx, hash, p),
		Overlay: overlay,
	}

	// README at this level, rendered from git or worktree per overlay state.
	for i := range listing.Entries {
		n := strings.ToLower(listing.Entries[i].Name)
		if listing.Entries[i].IsDir {
			continue
		}
		if n == "readme.md" || n == "readme.markdown" || n == "readme.txt" || n == "readme" {
			if bl, err := s.repo.Blob(ctx, hash, listing.Entries[i].Path, overlay); err == nil && len(bl.Content) <= 512<<10 {
				switch {
				case strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".markdown"):
					page.Readme = s.rnd.Markdown(bl.Content, s.linkRewriter(ref, hash, p))
				default:
					page.Readme = s.rnd.PlainText(bl.Content)
				}
				page.ReadmeName = listing.Entries[i].Name
			}
			break
		}
	}

	if p == "" {
		if lp, err := s.repo.Log(ctx, hash, 0, 6, "", false); err == nil {
			page.Recent = lp.Commits
		}
	}

	s.page(w, r, http.StatusOK, "tree", page)
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

	s.page(w, r, http.StatusOK, "blob", page)
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
	s.page(w, r, http.StatusOK, "blame", render.BlamePage{Base: s.newBase(ref, p, "blame"), Groups: groups})
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
	s.page(w, r, http.StatusOK, "commits", p)
}

func (s *Server) commit(w http.ResponseWriter, r *http.Request) {
	rev := strings.Trim(strings.TrimPrefix(r.URL.Path, "/commit"), "/")
	if rev == "" || strings.Contains(rev, "/") {
		s.errorPage(w, r, http.StatusNotFound, "not found", "A commit URL looks like /commit/{hash}.")
		return
	}
	meta, err := s.repo.CommitCtx(r.Context(), rev)
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
	page.Files = make([]render.FileCard, 0, len(diff.Files))
	for _, f := range diff.Files {
		card := render.FileCard{
			FileView: render.FileView{FileDiff: f},
			Ref:      meta.Hash,
			Side:     view == "side",
		}
		if !f.Binary && f.Patch != "" {
			card.Rendered = true
			card.Rows = git.UnifiedRows(f.Patch)
			if view == "side" {
				card.Sb = git.SideBySide(f.Patch)
			}
		}
		page.Adds += f.Adds
		page.Dels += f.Dels
		page.Files = append(page.Files, card)
	}
	page.SideURL = "/commit/" + rev + "?view=side"
	page.UnifiedURL = "/commit/" + rev

	s.page(w, r, http.StatusOK, "commit", page)
}

// compareTimeout bounds one comparison. The stat pass is cheap; the patch pass
// is bounded by bytes, and this deadline is the backstop that stops a huge range
// from becoming a request that never answers. Like search, it is not the page
// budget — the page says when the answer was cut short.
const compareTimeout = 10 * time.Second

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/compare"), "/")
	q := r.URL.Query()

	if rest == "" {
		if base, head := q.Get("base"), q.Get("head"); base != "" && head != "" {
			// The picker's form is a plain GET, so it arrives as a query rather
			// than a path; canonicalise it so the address bar shows what people
			// paste to each other.
			http.Redirect(w, r, compareURL(base, head, q.Get("view"), q.Get("path")), http.StatusFound)
			return
		}
		s.comparePicker(w, r)
		return
	}

	baseRef, headRef, ok := parseRange(rest)
	if !ok {
		s.errorPage(w, r, http.StatusBadRequest, "bad compare URL",
			"A compare URL looks like /compare/{base}...{head}. Three dots, because the diff is taken from the merge base, not from the two tips.")
		return
	}
	// One deadline covers resolving, scoping and the comparison itself: a page
	// with a budget must not spend it in a subprocess started without one.
	ctx, cancel := context.WithTimeout(r.Context(), compareTimeout)
	defer cancel()

	baseHash, err := s.repo.ResolveCtx(ctx, baseRef)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown base", "No branch, tag, or commit named "+baseRef+".")
		return
	}
	headHash, err := s.repo.ResolveCtx(ctx, headRef)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown head", "No branch, tag, or commit named "+headRef+".")
		return
	}

	// A scope that names nothing is dropped and the page says so; the same rule
	// search follows, for the same reason. Existence is asked of git, not go-git,
	// so a shared clone answers — and a path the head deleted but the base still
	// has is a legitimate thing to scope a review to.
	scope, ok := validQueryParamPath(q.Get("path"))
	scopeDropped := ""
	if !ok {
		scope = ""
	} else if scope != "" && !s.repo.ExistsAt(ctx, headHash, scope) && !s.repo.ExistsAt(ctx, baseHash, scope) {
		scopeDropped, scope = scope, ""
	}
	view := "unified"
	if q.Get("view") == "side" {
		view = "side"
	}

	rng, err := s.repo.Compare(ctx, baseHash, headHash, scope)
	if err != nil {
		if errors.Is(err, git.ErrObjectsUnavailable) {
			s.errorPage(w, r, http.StatusInternalServerError, "contents not available",
				"This comparison needs file contents that are not in this repository's object store — it looks like a partial clone — and gerve never fetches missing objects.")
			return
		}
		s.errorPage(w, r, http.StatusInternalServerError, "error", "Something went wrong comparing those refs.")
		return
	}
	patches := s.repo.RangePatch(ctx, rng.From, headHash, rng.Files)

	page := render.ComparePage{
		Base:         s.newBase(headRef, "", "compare"),
		BaseRef:      baseRef,
		HeadRef:      headRef,
		BaseInfo:     infoPtr(rng.Base),
		HeadInfo:     infoPtr(rng.Head),
		MergeBase:    commitPtr(rng.MergeBase),
		NoMergeBase:  rng.MergeBase == nil,
		Ahead:        rng.Ahead,
		Behind:       rng.Behind,
		Commits:      rng.Commits,
		CommitsMore:  rng.CommitsMore,
		FilesMore:    rng.FilesMore,
		FilesTotal:   len(rng.Files) + rng.FilesMore,
		Adds:         rng.Adds,
		Dels:         rng.Dels,
		Scoped:       scope,
		ScopeDropped: scopeDropped,
		WholeURL:     compareURL(baseRef, headRef, view, ""),
		View:         view,
		Stopped:      rng.Stopped || patches.Stopped,
		Truncated:    patches.Truncated,
		Unavailable:  patches.Unavailable,
		Failed:       patches.Failed,
		TooLarge:     rng.TooLarge,
		// A listing that could not be read in full is reported as such; the
		// files that did parse are still shown.
		FilesIncomplete: rng.FilesIncomplete,
		SwapURL:         compareURL(headRef, baseRef, view, scope),
		SideURL:         compareURL(baseRef, headRef, "side", scope),
		UnifiedURL:      compareURL(baseRef, headRef, "", scope),
	}
	page.Files = make([]render.FileCard, 0, len(rng.Files))
	for i, f := range rng.Files {
		card := render.FileCard{
			FileView: render.FileView{FileDiff: git.FileDiff{
				Status: f.Status, Old: f.Old, New: f.New,
				Adds: f.Adds, Dels: f.Dels, Binary: f.Binary,
			}},
			Ref:    headHash,
			Anchor: "file-" + strconv.Itoa(i+1),
			Side:   view == "side",
		}
		if d, ok := patches.Get(f); ok && d.Patch != "" {
			card.FileView.FileDiff = d
			card.Rendered = true
			if view == "side" {
				card.Sb = git.SideBySide(d.Patch)
			} else {
				card.Rows = git.UnifiedRows(d.Patch)
			}
		} else if !f.Binary {
			page.NotRendered++
		}
		page.Files = append(page.Files, card)
	}
	// The header's search box starts scoped to what the page is about, as it does
	// on the other pages; here that is the scope the comparison ran under.
	page.Scope = scope
	s.page(w, r, http.StatusOK, "compare", page)
}

// comparePicker is the chooser shown at /compare/ when no range was named, so
// the feature is reachable without knowing the URL grammar.
func (s *Server) comparePicker(w http.ResponseWriter, r *http.Request) {
	// Look for "main" in the ref list rather than resolving it: a picker default
	// is not worth a subprocess, and a master-only repo should not pay one.
	base := s.def
	if refs, err := s.repo.Refs(); err == nil {
		for _, rf := range refs {
			if rf.Kind == "branch" && rf.Name == "main" {
				base = "main"
				break
			}
		}
	}
	head := s.def
	if b := s.repo.Head().Branch; b != "" {
		head = b
	}
	s.page(w, r, http.StatusOK, "compare", render.ComparePage{
		Base:     s.newBase(s.def, "", "compare"),
		Pick:     true,
		PickBase: base,
		PickHead: head,
	})
}

// parseRange splits "{base}...{head}". Three dots and not two: git forbids ".."
// inside a ref name, so the separator cannot be ambiguous, and two-dot means
// tree-to-tree, which this page deliberately does not offer.
func parseRange(rest string) (base, head string, ok bool) {
	i := strings.Index(rest, "...")
	if i <= 0 || i+3 >= len(rest) {
		return "", "", false
	}
	base, head = rest[:i], rest[i+3:]
	for _, name := range []string{base, head} {
		for _, seg := range strings.Split(name, "/") {
			switch seg {
			case "", ".", "..", ".git":
				return "", "", false
			}
		}
	}
	return base, head, true
}

// compareURL builds /compare/{base}...{head}, escaping each ref segment so a
// slashed branch name stays a path rather than an escaped slash.
func compareURL(base, head, view, scope string) string {
	u := "/compare/" + escapeRefPath(base) + "..." + escapeRefPath(head)
	q := url.Values{}
	if view == "side" {
		q.Set("view", "side")
	}
	if scope != "" {
		q.Set("path", scope)
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

func escapeRefPath(name string) string {
	segs := strings.Split(name, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return strings.Join(segs, "/")
}

// infoPtr lets the template tell "no commit info" from a zero value.
func infoPtr(c git.CommitInfo) *git.CommitInfo {
	if c.Hash == "" {
		return nil
	}
	return &c
}

// commitPtr is infoPtr for a pointer that may already be nil.
func commitPtr(c *git.CommitInfo) *git.CommitInfo {
	if c == nil || c.Hash == "" {
		return nil
	}
	return c
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

// searchTimeout bounds one search. git grep over a large repository is seconds
// for a sparse pattern, so this is not the personas' page budget — that budget
// is about pages rendered from objects gerve already has. A deadline it can
// report beats a request that hangs without saying anything, and the results
// page says when it was cut short.
const searchTimeout = 10 * time.Second

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	ref := strings.Trim(strings.TrimPrefix(r.URL.Path, "/search"), "/")
	if ref == "" {
		ref = s.def
	}
	hash, err := s.repo.Resolve(ref)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "unknown ref", "No branch, tag, or commit named "+ref+".")
		return
	}

	q := r.URL.Query()
	// Trimmed once, here: the search would trim it anyway, and leaving the
	// untrimmed value to be displayed would silently disable the match
	// highlight (which looks for the query it was given, in the text).
	query := strings.TrimSpace(q.Get("q"))
	scope, ok := validQueryParamPath(q.Get("path"))
	if !ok || !s.repo.PathExists(r.Context(), hash, scope) {
		// A scope that names nothing — a typo, a glob, a path from another ref —
		// is dropped, and the results page says which scope it actually used, so
		// the widening is visible rather than silent.
		scope = ""
	}

	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	res, err := s.repo.Search(ctx, hash, s.repo.OverlayActive(hash), query, scope, git.SearchLimit)
	if err != nil {
		s.errorPage(w, r, http.StatusInternalServerError, "error", "Could not search this repository.")
		return
	}

	page := render.SearchPage{
		Base:    s.newBase(ref, "", "search"),
		Result:  res,
		Limit:   git.SearchLimit,
		Timeout: int(searchTimeout / time.Second),
	}
	// The scope and the query live in the query string, not the path, so the
	// scope the header box carries is the one just searched rather than a repo
	// path this page is "at".
	page.Scope = scope
	page.Query = query
	if scope != "" {
		page.WholeURL = render.URLFor("search", ref, "") + "?q=" + url.QueryEscape(query)
	}
	s.page(w, r, http.StatusOK, "search", page)
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
	s.page(w, r, http.StatusOK, "refs", page)
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
	if name == "" || strings.HasSuffix(name, "/") {
		// the embedded tree is only ever addressed file by file: no listings
		http.NotFound(w, r)
		return
	}
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
	// A control character in a path is not a path: it would reach git's argv and
	// fail there, turning a bad URL into a 500 instead of a page.
	if strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", false
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".", "..", ".git":
			return "", false
		}
	}
	return strings.Trim(p, "/"), true
}
