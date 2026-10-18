package render

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/b7r-dev/gerve/web"
)

// chroma emits a class per token type. A class with no rule in app.css is
// invisible in review — the token just inherits the surrounding ink — which is
// how the bash shebang (.cp) and shell variables (.nv) went unnoticed while
// every comment, keyword, and string was styled. This pins the palette against
// the lexers gerve can actually serve.
var (
	classAttrRe  = regexp.MustCompile(`class="([^"]+)"`)
	chromaRuleRe = regexp.MustCompile(`\.chroma \.([a-z0-9-]+)`)
)

// structural classes are chroma's own wrappers, not tokens: "line"/"cl" wrap a
// source line, and "plaincode"/"lt"/"ln" are the row/gutter cells we emit.
var nonTokenClasses = map[string]bool{
	"line": true, "cl": true, "hl": true,
	"highlight": true, "chroma": true,
	"plaincode": true, "lt": true, "ln": true,
}

func styledChromaClasses(t *testing.T) map[string]bool {
	t.Helper()
	css, err := fs.ReadFile(web.Static(), "static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	out := map[string]bool{}
	for _, m := range chromaRuleRe.FindAllSubmatch(css, -1) {
		out[string(m[1])] = true
	}
	if len(out) < 20 {
		t.Fatalf("only %d .chroma rules found — did app.css move?", len(out))
	}
	return out
}

func emittedClasses(html string) []string {
	var out []string
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, c := range strings.Fields(m[1]) {
			if nonTokenClasses[c] || strings.HasPrefix(c, "language-") {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

func TestChromaClassesAreAllStyled(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	styled := styledChromaClasses(t)

	// One sample per lexer family gerve is likely to meet, written to exercise
	// comments, keywords, strings, numbers, variables, selectors, and operators.
	sources := map[string]string{
		"main.go":     "package main\n\nimport \"fmt\"\n\n// greet the world\nfunc main() {\n\tname := \"world\"\n\tfmt.Printf(\"%s %d\\n\", name, 42)\n}\n",
		"run.sh":      "#!/usr/bin/env bash\n# a comment\nset -euo pipefail\ncd \"$(dirname \"$0\")/..\"\nfor f in *.txt; do echo \"${f}\"; done\n",
		"app.css":     "/* note */\n:root { --x: 1px; }\n#id, .cls { color: #fff !important; }\n@media (min-width: 40em) { body { margin: 0 auto; } }\n",
		"app.js":      "// note\nconst x = 1;\nfunction f(a) { return `t${a}`; }\nf(x);\n",
		"layout.tmpl": "{{if .X}}<div class=\"a\" id=\"b\">{{.Y}}</div>{{end}}\n",
		"go.mod":      "module example.com/x\n\ngo 1.26\n\nrequire github.com/yuin/goldmark v1.7.0\n",
		"conf.yaml":   "key: value\nlist:\n  - 1\n  - \"two\"\n# comment\n",
		"data.json":   "{\n  \"key\": [1, 2.5, true, null],\n  \"other\": \"text\"\n}\n",
		"tool.py":     "#!/usr/bin/env python3\nimport re\n\n# comment\ndef f(a: int) -> str:\n    return f\"{a + 1}\"\n",
		"q.sql":       "SELECT id, name FROM users WHERE age > 21 ORDER BY name;\n-- comment\n",
		"lib.rs":      "// note\npub fn f(a: u32) -> u32 {\n    let b = a + 1;\n    b\n}\n",
		"conf.toml":   "# note\n[table]\nkey = \"value\"\nnum = 42\n",
	}

	for name, src := range sources {
		out, ok := rd.Highlight([]byte(src), name)
		if !ok {
			t.Errorf("%s: not recognised by chroma (no highlighting)", name)
			continue
		}
		for _, cls := range emittedClasses(string(out)) {
			if !styled[cls] {
				t.Errorf("%s: chroma emits .%s but app.css has no .chroma .%s rule", name, cls, cls)
			}
		}
	}

	// Fenced code in markdown goes through a second highlighting path.
	md := []byte("```sh\n#!/bin/sh\necho \"$HOME\"\n```\n\n```css\n#a { color: red !important; }\n```\n")
	for _, cls := range emittedClasses(string(rd.Markdown(md, func(s string) string { return s }))) {
		if !styled[cls] {
			t.Errorf("markdown fence: chroma emits .%s but app.css has no .chroma .%s rule", cls, cls)
		}
	}
}

// Highlight used to hand the gutter and the code to chroma as two independent
// <pre> columns, which drift apart as soon as the two columns resolve even
// slightly different line boxes — numbers then sit beside the wrong lines. Rows
// make alignment structural, so this pins that shape.
func TestHighlightEmitsOneRowPerLine(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}

	// multi-line tokens (block comment, raw string) must not leak across rows
	src := "package main\n" +
		"\n" +
		"/*\n" +
		"a multi-line comment\n" +
		"still the comment\n" +
		"*/\n" +
		"\n" +
		"import \"fmt\"\n" +
		"\n" +
		"func main() {\n" +
		"\ts := `a raw string\n" +
		"spanning lines`\n" +
		"\tfmt.Println(s) // trailing comment\n" +
		"}\n"

	out, ok := rd.Highlight([]byte(src), "main.go")
	if !ok {
		t.Fatal("main.go was not highlighted")
	}
	body := string(out)

	wantLines := len(strings.Split(strings.TrimSuffix(src, "\n"), "\n"))
	if got := strings.Count(body, "<tr>"); got != wantLines {
		t.Errorf("rows = %d, want %d (one per source line)", got, wantLines)
	}
	if strings.Contains(body, `class="line"`) {
		t.Error("chroma's per-line spans should be unwrapped into rows")
	}
	if strings.Contains(body, "lntable") || strings.Contains(body, "lntd") {
		t.Error("the two-column line-number table is still present")
	}

	rows := regexp.MustCompile(`(?s)<tr><td class="ln">(\d+)</td><td class="lt">(.*?)</td></tr>`).FindAllStringSubmatch(body, -1)
	if len(rows) != wantLines {
		t.Fatalf("parsed %d rows, want %d", len(rows), wantLines)
	}
	for i, m := range rows {
		if m[1] != strconv.Itoa(i+1) {
			t.Fatalf("row %d numbered %q, want %d", i, m[1], i+1)
		}
		if open, closed := strings.Count(m[2], "<span"), strings.Count(m[2], "</span>"); open != closed {
			t.Errorf("row %d spans unbalanced: %d open, %d close", i+1, open, closed)
		}
		// A newline left at the end of a cell adds a second line box, so filled
		// rows would stand taller than blank ones. Whatever chroma does with its
		// own whitespace tokens, no cell may end in a bare newline.
		if strings.HasSuffix(m[2], "\n") {
			t.Errorf("row %d cell ends with a raw newline: %q", i+1, m[2])
		}
	}
}
