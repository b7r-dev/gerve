// Package render turns repository data into HTML: html/template pages,
// goldmark markdown, and chroma syntax highlighting, all with embedded
// assets and zero runtime dependencies on the outside world.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/url"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"

	"b7r.dev/gerve/web"
)

const (
	maxHighlight = 512 << 10 // bytes highlighted; larger falls back to plain
)

// Renderer owns parsed templates and generated highlighting CSS.
type Renderer struct {
	pages map[string]*template.Template
	md    goldmark.Markdown
	chroma template.CSS
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
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)

	return &Renderer{pages: pages, md: md, chroma: chromaCSS()}, nil
}

// Page renders a named page with the shared layout.
func (rd *Renderer) Page(w io.Writer, name string, data any) {
	if err := rd.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// ChromaCSS returns the two-theme scoped highlighting stylesheet.
func (rd *Renderer) ChromaCSS() template.CSS { return rd.chroma }

// Markdown renders markdown to safe HTML (raw HTML in source is escaped).
func (rd *Renderer) Markdown(src []byte) template.HTML {
	var b bytes.Buffer
	if err := rd.md.Convert(src, &b); err != nil {
		return template.HTML("<pre class=\"readme-plain\">" + template.HTMLEscapeString(string(src)) + "</pre>")
	}
	return template.HTML(b.String())
}

// PlainText wraps preformatted text as safe HTML.
func (rd *Renderer) PlainText(src []byte) template.HTML {
	return template.HTML("<pre class=\"readme-plain\">" + template.HTMLEscapeString(string(src)) + "</pre>")
}

// Highlight renders source code with chroma, using line-number tables. ok is
// false when no lexer matched the filename; callers fall back to PlainCode.
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
	formatter := chromahtml.New(
		chromahtml.WithClasses(true),
		chromahtml.WithLineNumbers(true),
		chromahtml.LineNumbersInTable(true),
	)
	style := styles.Get("github")
	var b bytes.Buffer
	if err := formatter.Format(&b, style, it); err != nil {
		return "", false
	}
	return template.HTML(b.String()), true
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

// --- chroma CSS generation --------------------------------------------------

// chromaCSS generates class-based highlighting styles for both themes and
// scopes them under html.light / html.dark so the theme toggle restyles
// code without reloading.
func chromaCSS() template.CSS {
	var b strings.Builder
	for _, th := range []struct{ cls, style string }{
		{"light", "github"},
		{"dark", "github-dark"},
	} {
		st := styles.Get(th.style)
		if st == nil {
			continue
		}
		var cb bytes.Buffer
		f := chromahtml.New()
		if err := f.WriteCSS(&cb, st); err != nil {
			continue
		}
		b.WriteString(scopeCSS(cb.String(), "html."+th.cls))
	}
	return template.CSS(b.String())
}

// scopeCSS prefixes every selector in a stylesheet with scope.
func scopeCSS(css, scope string) string {
	var b strings.Builder
	for _, rule := range strings.Split(css, "}") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		idx := strings.Index(rule, "{")
		if idx < 0 {
			continue
		}
		sel, body := rule[:idx], rule[idx+1:]
		var sels []string
		for _, s := range strings.Split(sel, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			sels = append(sels, scope+" "+s)
		}
		b.WriteString(strings.Join(sels, ", "))
		b.WriteString(" {")
		b.WriteString(body)
		b.WriteString("}\n")
	}
	return b.String()
}

// ensure fs import used (ParseFS takes fs.FS via embed.FS)
var _ fs.FS = web.Templates()
