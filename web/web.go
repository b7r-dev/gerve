// Package web embeds every template and static asset so the gerve binary
// is fully self-contained: no build step, no network fetches. Fonts
// (IBM Plex, SIL OFL 1.1) are vendored under static/fonts with their
// license alongside.
package web

import (
	"embed"
	"mime"
)

var (
	//go:embed templates
	templates embed.FS
	//go:embed static
	static embed.FS
)

func init() {
	// Go's builtin table predates woff2; without this, FileServer would
	// serve fonts as application/octet-stream.
	mime.AddExtensionType(".woff2", "font/woff2")
	mime.AddExtensionType(".woff", "font/woff")
}

// Templates returns the embedded template filesystem (templates/).
func Templates() embed.FS { return templates }

// Static returns the embedded static filesystem (static/).
func Static() embed.FS { return static }
