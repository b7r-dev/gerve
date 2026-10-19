package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
)

// doReq issues a GET with optional request headers and returns everything the
// cache tests need to assert on.
func doReq(t *testing.T, url string, hdr map[string]string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

// TestPagesRevalidate guards the caching contract: a rendered page must not be
// left to heuristic caching, and a page the browser already holds must be
// confirmable with a bare 304 — which is what stops a previous build's
// /static/*?v=… URLs from outliving the build that produced them.
func TestPagesRevalidate(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"README.md": "hi\n", "sub/x.go": "package sub\n"})
	ts, _ := newTestServer(t, dir)

	paths := []string{
		"/tree/HEAD/",
		"/tree/HEAD/sub/",
		"/blob/HEAD/README.md",
		"/blame/HEAD/README.md",
		"/commits/HEAD",
		"/refs",
	}
	for _, path := range paths {
		status, hdr, body := doReq(t, ts.URL+path, nil)
		if status != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, status)
			continue
		}
		if got := hdr.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, "no-cache")
		}
		if got := hdr.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", path, got)
		}
		if len(body) == 0 {
			t.Errorf("%s: response was empty", path)
		}
		if hdr.Get("ETag") == "" {
			t.Errorf("%s: no ETag", path)
		}
	}
}

// TestRevalidationAnswers304 needs a page whose bytes cannot move on their own.
// Every content page above embeds a relative time ("2 minutes ago"), which is
// legitimately a different page once the minute ticks — so a 304 asserted there
// would be a test that passes almost always and fails once in ten thousand runs.
// The empty-repo page carries no clock, so its validator is stable by
// construction. Pages with a clock are the deliberate exception: content that
// really did change should be re-sent, and the alternative — freezing "ago" so
// the validator holds — would make the page lie about the time instead.
func TestRevalidationAnswers304(t *testing.T) {
	dir := initTestRepo(t) // no commits: the empty-repository page
	ts, _ := newTestServer(t, dir)

	status, hdr, body := doReq(t, ts.URL+"/", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, "no commits yet") {
		t.Fatalf("not the empty-repository page: %.120s", body)
	}
	// The property that makes this page usable for a 304 assertion: no clock in
	// the bytes.
	if strings.Contains(body, `class="time"`) {
		t.Fatal("the empty-repository page renders a relative time after all")
	}
	tag := hdr.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}

	status, _, revalidated := doReq(t, ts.URL+"/", map[string]string{"If-None-Match": tag})
	if status != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", status)
	}
	if revalidated != "" {
		t.Errorf("304 carried a %d-byte body", len(revalidated))
	}
}

// TestWrongValidatorGetsThePage: only the exact tag revalidates. A stale tag, a
// truncated one, or another page's tag must all produce the page itself.
func TestWrongValidatorGetsThePage(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	ts, _ := newTestServer(t, dir)

	_, hdr, _ := doReq(t, ts.URL+"/tree/HEAD/", nil)
	tag := hdr.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}
	_, otherHdr, _ := doReq(t, ts.URL+"/refs", nil)

	wrong := []string{
		`"0000000000000000"`, // well formed, not this page
		tag[:len(tag)-1],     // truncated
		"L" + tag,            // mangled
		otherHdr.Get("ETag"), // a different page's tag
	}
	for _, w := range wrong {
		status, _, body := doReq(t, ts.URL+"/tree/HEAD/", map[string]string{"If-None-Match": w})
		if status != http.StatusOK {
			t.Errorf("If-None-Match %q: status = %d, want 200", w, status)
		}
		if !strings.Contains(body, "</html>") {
			t.Errorf("If-None-Match %q: body was not the page", w)
		}
	}
}

// TestHeadRequestCarriesTheCacheContract: Go's mux routes HEAD through the GET
// patterns, so the same headers must reach a HEAD — with no body.
func TestHeadRequestCarriesTheCacheContract(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	ts, _ := newTestServer(t, dir)

	req, err := http.NewRequest(http.MethodHead, ts.URL+"/tree/HEAD/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("no ETag on a HEAD")
	}
	if len(body) != 0 {
		t.Errorf("HEAD returned %d bytes", len(body))
	}
}

// TestPageRenderFailureIs500: a render that fails must not be served as a 200
// with a truncated body. Buffering is what makes that possible — nothing has
// reached the client, so the status is still the caller's to choose — and the
// failure has to survive the error page being the thing that failed.
func TestPageRenderFailureIs500(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	repo, err := git.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rnd, err := render.New()
	if err != nil {
		t.Fatal(err)
	}
	s := New(repo, rnd, Options{Version: "test"})

	rec := httptest.NewRecorder()
	s.page(rec, httptest.NewRequest(http.MethodGet, "/tree/HEAD/", nil), http.StatusOK, "no-such-page", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on the failure page")
	}

	// With the error page itself failing there is nowhere left to render, so
	// that response is written by hand — and must still be a 500.
	rec = httptest.NewRecorder()
	s.renderFailure(rec, httptest.NewRequest(http.MethodGet, "/", nil), "error")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("hand-written fallback status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "500") {
		t.Errorf("hand-written fallback body = %q", rec.Body.String())
	}
}

// TestPageETagStability: the validator is derived from the bytes, so identical
// content answers with the same tag across requests.
//
// Deliberately a page without relative times: "2 minutes ago" is part of the
// rendered bytes, so a page carrying one is legitimately a different page a
// minute later. The tag follows content, not the clock — which also means it
// stays valid across a server restart, since nothing about its derivation
// changes.
func TestPageETagStability(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	ts, _ := newTestServer(t, dir)

	_, hdr1, body1 := doReq(t, ts.URL+"/missing", nil)
	_, hdr2, body2 := doReq(t, ts.URL+"/missing", nil)
	if hdr1.Get("ETag") == "" {
		t.Fatal("no ETag on the error page")
	}
	if hdr1.Get("ETag") != hdr2.Get("ETag") {
		t.Errorf("ETag not stable across requests: %q then %q", hdr1.Get("ETag"), hdr2.Get("ETag"))
	}
	if body1 != body2 {
		t.Error("identical requests produced different bodies")
	}
}

// TestStaleETagIsNotHonoured is the point of the whole exercise: a page a
// browser kept from an earlier state must not be confirmed as current. A tag
// captured before a new commit must answer 200 with the new content, never 304
// — otherwise the browser goes on rendering a page whose /static/*?v=… URLs
// belong to a build that no longer exists. The body is asserted as well as the
// status, so a tag that moved for some unrelated reason cannot pass this.
func TestStaleETagIsNotHonoured(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	ts, _ := newTestServer(t, dir)

	status, hdr, _ := doReq(t, ts.URL+"/commits/HEAD", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	stale := hdr.Get("ETag")
	if stale == "" {
		t.Fatal("no ETag on the commits page")
	}

	commitFiles(t, dir, "two", map[string]string{"file.txt": "v2\n"})

	status, hdr, body := doReq(t, ts.URL+"/commits/HEAD", map[string]string{"If-None-Match": stale})
	if status != http.StatusOK {
		t.Errorf("stale tag revalidated: status = %d, want 200", status)
	}
	if hdr.Get("ETag") == stale {
		t.Error("ETag unchanged after a new commit")
	}
	if !strings.Contains(body, "two") {
		t.Error("body does not show the new commit")
	}
}

// TestErrorStatusIsNotRevalidated: a cached 404 must never be confirmed as
// current just because its bytes have not changed.
func TestErrorStatusIsNotRevalidated(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\n"})
	ts, _ := newTestServer(t, dir)

	status, hdr, _ := doReq(t, ts.URL+"/tree/HEAD/nope/", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if got := hdr.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("404 Cache-Control = %q, want no-cache", got)
	}
	tag := hdr.Get("ETag")
	if tag == "" {
		t.Fatal("404 carried no ETag")
	}
	status, _, body := doReq(t, ts.URL+"/tree/HEAD/nope/", map[string]string{"If-None-Match": tag})
	if status != http.StatusNotFound {
		t.Errorf("revalidated status = %d, want 404 (a 404 is never a 304)", status)
	}
	if !strings.Contains(body, "not found") {
		t.Error("404 body missing after revalidation attempt")
	}
}

func TestETagMatches(t *testing.T) {
	tag := `"abc123"`
	cases := []struct {
		header string
		want   bool
	}{
		{tag, true},
		{"W/" + tag, true}, // If-None-Match compares weakly
		{`"other", ` + tag, true},
		{tag + `, "other"`, true},
		{"*", true},
		{"", false},
		{`"other"`, false},
		{`"abc1234"`, false}, // longer tag is not a prefix match
	}
	for _, c := range cases {
		if got := etagMatches(c.header, tag); got != c.want {
			t.Errorf("etagMatches(%q, %q) = %v, want %v", c.header, tag, got, c.want)
		}
	}
}

func TestContentTag(t *testing.T) {
	a, b := contentTag([]byte("hello")), contentTag([]byte("hello"))
	if a != b {
		t.Errorf("contentTag not stable: %q then %q", a, b)
	}
	if len(a) < 4 || a[0] != '"' || a[len(a)-1] != '"' {
		t.Errorf("contentTag %q is not a quoted entity tag", a)
	}
	if a == contentTag([]byte("hello!")) {
		t.Error("contentTag ignored a content change")
	}
}
