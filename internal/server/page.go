package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
// restart and changes exactly when the page does.
func (s *Server) page(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var body bytes.Buffer
	s.rnd.Page(&body, name, data)
	tag := contentTag(body.Bytes())

	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", tag)

	// Only a successful render is revalidatable: a cached 404 must never be
	// confirmed as current merely because its bytes have not changed.
	if status == http.StatusOK && etagMatches(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body.Bytes())
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
