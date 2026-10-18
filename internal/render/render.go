// Package render turns repository data into HTML: html/template pages,
// goldmark markdown, and chroma syntax highlighting, all with embedded
// assets and zero runtime dependencies on the outside world.
package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/b7r-dev/gerve/web"
)

const (
	maxHighlight = 512 << 10 // bytes highlighted; larger falls back to plain
)

// Renderer owns parsed templates and the markdown pipeline.
type Renderer struct {
	pages map[string]*template.Template
	md    goldmark.Markdown

	// assetHash maps embedded static paths to short content hashes, and
	// css is app.css with its font URLs versioned by those hashes.
	assetHash map[string]string
	css       []byte
}

// New parses templates and prepares markdown + chroma CSS.
func New() (*Renderer, error) {
	funcs := template.FuncMap{
		"short":         shortHash,
		"timeago":       timeago,
		"shortdate":     func(t time.Time) string { return t.Format("2006-01-02") },
		"sizefmt":       humanBytes,
		"urlFor":        URLFor,
		"joinPath":      joinPath,
		"urlPathEscape": url.PathEscape,
		"kindClass":     kindClass,
		"initial":       initial,
	}

	hashes, err := hashStatic()
	if err != nil {
		return nil, fmt.Errorf("hash static assets: %w", err)
	}
	funcs["asset"] = func(name string) string { return assetURL(name, hashes) }

	root := template.New("gerve").Funcs(funcs)
	if _, err := root.ParseFS(web.Templates(), "templates/layout.tmpl", "templates/partial-*.tmpl"); err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	pages := map[string]*template.Template{}
	for _, name := range []string{"tree", "blob", "blame", "commits", "commit", "refs", "error", "empty"} {
		t, err := root.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := t.ParseFS(web.Templates(), "templates/"+name+".tmpl"); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		pages[name] = t
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)

	return &Renderer{pages: pages, md: md, assetHash: hashes, css: versionCSS(hashes)}, nil
}

// asset returns a static URL carrying the file's content hash, so a new
// binary run never serves a stale stylesheet, script, or font.
func assetURL(name string, hashes map[string]string) string {
	if h, ok := hashes[name]; ok {
		return "/static/" + name + "?v=" + h
	}
	return "/static/" + name
}

func hashStatic() (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(web.Static(), "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(web.Static(), p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:])[:8]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

var fontURLRe = regexp.MustCompile(`url\("/static/(fonts/[^"?]+)"\)`)

// versionCSS rewrites the font URLs inside app.css to carry content hashes,
// closing the last stale-asset hole (a re-vendored font under the same name).
func versionCSS(hashes map[string]string) []byte {
	src, err := fs.ReadFile(web.Static(), "static/app.css")
	if err != nil {
		return nil
	}
	s := fontURLRe.ReplaceAllStringFunc(string(src), func(m string) string {
		sub := fontURLRe.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		if h, ok := hashes[sub[1]]; ok {
			return `url("/static/` + sub[1] + "?v=" + h + `")`
		}
		return m
	})
	return []byte(s)
}

// AssetVersion returns the content hash of an embedded static file (or "").
func (rd *Renderer) AssetVersion(name string) string { return rd.assetHash[name] }

// VersionedCSS returns app.css with content-hashed font URLs.
func (rd *Renderer) VersionedCSS() []byte { return rd.css }

// Page renders a named page with the shared layout.
func (rd *Renderer) Page(w io.Writer, name string, data any) {
	if err := rd.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// Markdown renders markdown to safe HTML (raw HTML in source is escaped).
// Soft line breaks collapse — single newlines in the source render as
// spaces, matching GitHub's rendering of .md files.
//
// rewrite, when non-nil, receives each relative link/image destination and
// returns a replacement URL ("" keeps the original); gerve uses it to point
// repo-relative links at its own routes with the current ref pinned.
func (rd *Renderer) Markdown(src []byte, rewrite func(string) string) template.HTML {
	doc := rd.md.Parser().Parse(text.NewReader(src))
	if rewrite != nil {
		ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := n.(type) {
			case *ast.Link:
				n.Destination = rewriteDest(n.Destination, rewrite)
			case *ast.Image:
				n.Destination = rewriteDest(n.Destination, rewrite)
			}
			return ast.WalkContinue, nil
		})
	}
	var b bytes.Buffer
	if err := rd.md.Renderer().Render(&b, src, doc); err != nil {
		return template.HTML("<pre class=\"readme-plain\">" + template.HTMLEscapeString(string(src)) + "</pre>")
	}
	return template.HTML(wrapTables(b.String()))
}

// wrapTables puts each markdown table in its own scroll container so a wide
// table scrolls sideways inside the card instead of squeezing its columns into
// tall wrapped cells (see .md .table-scroll in app.css). Goldmark emits bare
// <table> elements with no attributes, and GFM tables cannot nest inside one
// another, so wrapping every open/close tag stays balanced.
func wrapTables(html string) string {
	if !strings.Contains(html, "<table>") {
		return html
	}
	out := strings.ReplaceAll(html, "<table>", `<div class="table-scroll"><table>`)
	return strings.ReplaceAll(out, "</table>", "</table></div>")
}

// rewriteDest maps one markdown destination through rewrite, leaving
// absolute URLs, schemes, and pure anchors alone; fragments survive.
func rewriteDest(dest []byte, rewrite func(string) string) []byte {
	href := string(dest)
	if href == "" || strings.HasPrefix(href, "#") ||
		strings.Contains(href, "://") ||
		strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
		return dest
	}
	base, frag := splitFragment(href)
	mapped := rewrite(base)
	if mapped == "" || mapped == base {
		return dest
	}
	return []byte(mapped + frag)
}

func splitFragment(href string) (base, frag string) {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		return href[:i], href[i:]
	}
	return href, ""
}

// PlainText wraps preformatted text as safe HTML.
func (rd *Renderer) PlainText(src []byte) template.HTML {
	return template.HTML("<pre class=\"readme-plain\">" + template.HTMLEscapeString(string(src)) + "</pre>")
}

// Highlight renders source code with chroma as one numbered row per line,
// the same shape PlainCode uses. ok is false when no lexer matched the
// filename; callers fall back to PlainCode.
//
// Chroma can put the gutter and the code in two independent <pre> columns
// (LineNumbersInTable), which only line up for as long as both columns compute
// exactly the same line box — one font substitution, zoom level, or rounding
// difference and every number below the first drift lands beside the wrong
// line. Emitting a row per line makes alignment structural instead.
func (rd *Renderer) Highlight(content []byte, filename string) (out template.HTML, ok bool) {
	if len(content) > maxHighlight {
		return "", false
	}
	lexer := lexers.Match(filename)
	if lexer == nil {
		return "", false
	}
	it, err := lexer.Tokenise(nil, string(content))
	if err != nil {
		return "", false
	}
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	style := styles.Get("github")
	var b bytes.Buffer
	if err := formatter.Format(&b, style, it); err != nil {
		return "", false
	}

	var sb strings.Builder
	sb.WriteString(`<div class="plaincode chroma"><table>`)
	for i, ln := range chromaLines(b.String()) {
		fmt.Fprintf(&sb, `<tr><td class="ln">%d</td><td class="lt">%s</td></tr>`, i+1, ln)
	}
	sb.WriteString(`</table></div>`)
	return template.HTML(sb.String()), true
}

const chromaLineOpen = `<span class="line">`

// chromaLines splits chroma's per-line spans into one HTML fragment per source
// line. Chroma closes its token spans at every newline, so snipping the line
// wrapper leaves each fragment balanced on its own.
func chromaLines(out string) []string {
	body := out
	if i := strings.Index(body, "<code>"); i >= 0 {
		body = body[i+len("<code>"):]
	}
	if i := strings.LastIndex(body, "</code>"); i >= 0 {
		body = body[:i]
	}
	parts := strings.Split(body, chromaLineOpen)
	if len(parts) < 2 {
		return nil
	}
	lines := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		// chroma closes the line span *after* the newline, so the close tag
		// comes off first; leaving the newline would add a second line box
		p = strings.TrimSuffix(p, "</span>")
		p = strings.TrimSuffix(p, "\n")
		lines = append(lines, p)
	}
	// a trailing newline in the source yields one empty trailing line
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// PlainCode renders escaped source with a line-number table, no highlighting.
func (rd *Renderer) PlainCode(content []byte) template.HTML {
	lines := strings.Split(string(content), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var b strings.Builder
	b.WriteString(`<div class="plaincode"><table>`)
	for i, ln := range lines {
		fmt.Fprintf(&b, `<tr><td class="ln">%d</td><td class="lt">%s</td></tr>`, i+1, template.HTMLEscapeString(ln))
	}
	b.WriteString(`</table></div>`)
	return template.HTML(b.String())
}

// URLFor builds a route URL with a (possibly slashed) ref and repo path,
// escaping every segment.
func URLFor(kind, ref, path string) string {
	var b strings.Builder
	b.WriteByte('/')
	b.WriteString(kind)
	b.WriteByte('/')
	b.WriteString(url.PathEscape(ref))
	if path != "" {
		for _, seg := range strings.Split(path, "/") {
			b.WriteByte('/')
			b.WriteString(url.PathEscape(seg))
		}
	}
	return b.String()
}

// --- template helpers -------------------------------------------------------

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func kindClass(k byte) string {
	switch k {
	case '+':
		return "add"
	case '-':
		return "del"
	case '@':
		return "hunk"
	case ' ':
		return "ctx"
	default:
		return "none"
	}
}

func initial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func timeago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	future := d < 0
	if future {
		d = -d
	}
	s := "just now"
	switch {
	case d < time.Minute:
	case d < time.Hour:
		n := int(d.Minutes())
		s = plural(n, "minute")
	case d < 24*time.Hour:
		n := int(d.Hours())
		s = plural(n, "hour")
	case d < 30*24*time.Hour:
		n := int(d.Hours() / 24)
		s = plural(n, "day")
	case d < 365*24*time.Hour:
		n := int(d.Hours() / 24 / 30)
		s = plural(n, "month")
	default:
		n := int(d.Hours() / 24 / 365)
		s = plural(n, "year")
	}
	if s == "just now" {
		return s
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func humanBytes(n int64) string {
	if n < 0 {
		return ""
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	const unit = 1024
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
