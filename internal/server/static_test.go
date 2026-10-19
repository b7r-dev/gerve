package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b7r-dev/gerve/internal/render"
)

func staticTestServer(t *testing.T) (*httptest.Server, *render.Renderer) {
	t.Helper()
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "x\n"})
	return newTestServer(t, dir)
}

func getBody(t *testing.T, url string) (string, http.Header) {
	t.Helper()
	_, hdr, body := doReq(t, url, nil)
	return body, hdr
}

// TestAssetsAreContentVersioned pins the cache-busting contract: pages
// reference hashed asset URLs, hashed URLs are immutable, unhashed URLs
// revalidate, and the stylesheet's font URLs are versioned too.
func TestAssetsAreContentVersioned(t *testing.T) {
	ts, rnd := staticTestServer(t)

	cssHash := rnd.AssetVersion("app.css")
	jsHash := rnd.AssetVersion("app.js")
	if cssHash == "" || jsHash == "" {
		t.Fatalf("missing content hashes: css=%q js=%q", cssHash, jsHash)
	}

	body, _ := getBody(t, ts.URL+"/")
	for _, want := range []string{
		`href="/static/app.css?v=` + cssHash + `"`,
		`src="/static/app.js?v=` + jsHash + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not reference versioned asset %q", want)
		}
	}

	if _, h := getBody(t, ts.URL+"/static/app.css?v="+cssHash); !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Errorf("hashed asset not immutable: %q", h.Get("Cache-Control"))
	}
	if _, h := getBody(t, ts.URL+"/static/app.css"); !strings.Contains(h.Get("Cache-Control"), "no-cache") {
		t.Errorf("unhashed asset should revalidate: %q", h.Get("Cache-Control"))
	}

	css, h := getBody(t, ts.URL+"/static/app.css?v="+cssHash)
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("css content-type: %q", ct)
	}
	if !strings.Contains(css, "IBMPlexSans-Regular.woff2?v=") {
		t.Error("stylesheet font URLs are not content-versioned")
	}
	if h := strings.Count(css, "/static/fonts/"); h == 0 || strings.Count(css, "?v=") < h {
		t.Errorf("expected every font URL versioned: fonts=%d versions=%d", h, strings.Count(css, "?v="))
	}
}

// TestStaticHasNoDirectoryListing keeps the embedded tree addressed file by
// file: /static/ and its subdirectories must not enumerate their contents.
func TestStaticHasNoDirectoryListing(t *testing.T) {
	ts, _ := staticTestServer(t)
	for _, path := range []string{"/static/", "/static/fonts/"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
		if strings.Contains(string(body), "app.css") || strings.Contains(string(body), "woff2") {
			t.Errorf("%s: listing leaked asset names", path)
		}
	}
	// a real file still serves
	if resp, err := http.Get(ts.URL + "/static/app.js"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("/static/app.js: status %d, want 200", resp.StatusCode)
		}
	}
}
