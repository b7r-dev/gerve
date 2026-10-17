// Package web embeds every template and static asset so the gerve binary
// is fully self-contained: no build step, no network fetches.
package web

import "embed"

var (
	//go:embed templates
	templates embed.FS
	//go:embed static
	static embed.FS
)

// Templates returns the embedded template filesystem (templates/).
func Templates() embed.FS { return templates }

// Static returns the embedded static filesystem (static/).
func Static() embed.FS { return static }
