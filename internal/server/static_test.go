package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
)

func staticTestServer(t *testing.T) (*httptest.Server, *render.Renderer) {
	t.Helper()
	dir := t.TempDir()
	gitHelper(t, dir, "init", "-q", "-b", "main")
	gitHelper(t, dir, "config", "user.email", "t@example.com")
	gitHelper(t, dir, "config", "user.name", "T")
	if err := os.WriteFile(dir+"/file.txt", []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitHelper(t, dir, "add", "-A")
	gitHelper(t, dir, "commit", "-q", "-m", "one")

	repo, err := git.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rnd, err := render.New()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(repo, rnd, Options{Version: "test"}).Handler())
	t.Cleanup(ts.Close)
	return ts, rnd
}

func getBody(t *testing.T, url string) (string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), resp.Header
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
