package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
)

// Rendered pages carried no cache policy at all, and a browser's heuristic
// caching decides in that vacuum. The failure is quiet but real: a page held
// over from a previous build keeps pointing at that build's /static/*?v=…
// URLs, so a server that has been fixed goes on looking broken until the
// heuristic happens to expire. That is exactly how the iOS text-size fix first
// appeared not to have landed.
//
// "no-cache" stores the page but requires revalidation, which is the shape
// gerve wants: content-hashed assets keep their year-long immutable cache,
// while the HTML that references them is checked on every navigation. The ETag
// makes the check cheap — unchanged content answers 304 with no body — and
// because it is derived from the bytes rather than the clock it survives a
// restart and changes exactly when the bytes do.
//
// Bytes, not facts: a page carrying a relative time ("2 minutes ago") is
// genuinely a different page once the minute ticks, so those pages revalidate
// to a full 200 rather than a 304. That is the honest outcome — the text really
// did change, and holding the validator steady would mean freezing the "ago"
// and lying about the time instead.
func (s *Server) page(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var body bytes.Buffer
	if err := s.rnd.Page(&body, name, data); err != nil {
		// Nothing has reached the client yet — the body is still in the buffer —
		// so a failed render can be an honest 500 instead of a truncated page
		// that a stable validator would go on confirming as current.
		log.Printf("render %s: %v", name, err)
		s.renderFailure(w, r, name)
		return
	}
	tag := contentTag(body.Bytes())

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", tag)

	// Only a 200 is revalidated: a cached 404 must never be confirmed as current
	// merely because its bytes have not changed.
	if status == http.StatusOK && etagMatches(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body.Bytes())
}

// renderFailure answers a failed render with a 500, carrying the same cache
// directives as every other page so a broken page cannot be cached either. When
// the error page itself is what failed there is nowhere left to render, so that
// one case is written by hand — the only response in gerve that is.
func (s *Server) renderFailure(w http.ResponseWriter, r *http.Request, name string) {
	if name != "error" {
		s.errorPage(w, r, http.StatusInternalServerError, "internal error",
			"Something went wrong rendering this page. The server is still running.")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte("500 internal error\n"))
}

// contentTag is a strong validator for a rendered body: eight bytes of SHA-256,
// quoted, over the bytes alone.
func contentTag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// etagMatches reports whether an If-None-Match value names tag. The value is a
// comma-separated list, "*" is a wildcard, and a weak prefix still counts —
// If-None-Match uses weak comparison.
func etagMatches(header, tag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == tag {
			return true
		}
	}
	return false
}
