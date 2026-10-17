package server

import "testing"

func TestRepoRelative(t *testing.T) {
	cases := []struct {
		base, href, want string
		ok               bool
	}{
		{"", "docs/personas", "docs/personas", true},
		{"", "docs/personas/", "docs/personas", true},   // trailing slash collapses
		{"", "./README.md", "README.md", true},          // explicit current dir
		{"docs", "personas/00.md", "docs/personas/00.md", true},
		{"a/b", "../c.md", "a/c.md", true},              // one level up
		{"a/b", "../../top.md", "top.md", true},         // up to root
		{"a/b", "../../../escape.md", "", false},        // escapes the repo
		{"", "/root.md", "root.md", true},               // root-relative
		{"", ".git/config", "", false},                  // .git forbidden
		{"", "a/../../.git/config", "", false},
		{"", "", "", false},
		{"", "file.md?download=1", "file.md", true},     // query stripped
		{"", "my%20file.md", "my file.md", true},        // percent-decoded
	}
	for _, c := range cases {
		got, ok := repoRelative(c.base, c.href)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("repoRelative(%q, %q) = (%q, %v), want (%q, %v)",
				c.base, c.href, got, ok, c.want, c.ok)
		}
	}
}
