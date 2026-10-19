package render

import (
	"html/template"

	"github.com/b7r-dev/gerve/internal/git"
)

// Base is embedded by every page's data struct; layout.tmpl relies on it.
type Base struct {
	Version  string
	RepoName string
	Ref      string
	Head     git.HeadInfo
	Refs     []git.RefInfo
	Route    string // route name for the ref switcher: tree|blob|blame|search
	Path     string // the repo path this page is about (crumbs, ref-pinned links)
	// Scope is what the header's search box carries as its default: the path
	// you are looking at, so a search started from a directory searches that
	// directory, and the results page carries the scope it just used. Empty
	// means the whole repository.
	Scope  string
	Query  string // the search box's current value, filled on the results page
	Crumbs []Crumb
}

// Crumb is one breadcrumb segment.
type Crumb struct {
	Name, URL string
}

type TreePage struct {
	Base
	Entries    []git.Entry
	DirLast    *git.CommitInfo
	Overlay    bool
	Readme     template.HTML // set only when a README exists
	ReadmeName string
	Recent     []git.CommitInfo
}

type BlobPage struct {
	Base
	Blob       *git.Blob
	Change     git.ChangeKind
	Last       *git.CommitInfo
	Lines      int
	Large      bool
	Empty      bool
	IsMD       bool
	Code       template.HTML
	SourceURL  string // set when showing rendered markdown: link to source
	RenderURL  string // set when showing source: link to rendered view
	CanBlame   bool
	CanHistory bool
}

type BlamePage struct {
	Base
	Groups []git.BlameGroup
}

type CommitsPage struct {
	Base
	Log     *git.LogPage
	Page    int
	Path    string
	Follow  bool
	PrevURL string
	NextURL string
}

// SearchPage renders results for one query, grouped by file. The scope is
// Base.Scope — the same field the header's search box carries, since on this
// page they are the same fact.
type SearchPage struct {
	Base
	// WholeURL repeats this query with no scope, so a scoped search that found
	// nothing can offer the wider one in a click.
	WholeURL string
	Result   *git.SearchResult
	Limit    int
	Timeout  int // seconds; the deadline the search ran under
}

// FileView wraps one file's diff with precomputed render structures.
type FileView struct {
	git.FileDiff
	Rows []git.Row      // unified rows
	Sb   []git.SideHunk // side-by-side hunks
}

type CommitPage struct {
	Base
	Meta       *git.CommitMeta
	Files      []FileView
	Adds, Dels int
	View       string // "unified" | "side"
	Merge      bool

	SideURL    string
	UnifiedURL string
}

type RefsEntry struct {
	git.RefInfo
	Info git.CommitInfo
}

type RefsPage struct {
	Base
	Branches, Tags []RefsEntry
}

type ErrorPage struct {
	Base
	Code           int
	Title, Message string
}

type EmptyPage struct {
	Base
}
