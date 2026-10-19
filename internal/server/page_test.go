package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
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
		tag := hdr.Get("ETag")
		if tag == "" {
			t.Errorf("%s: no ETag", path)
			continue
		}

		status, _, revalidated := doReq(t, ts.URL+path, map[string]string{"If-None-Match": tag})
		if status != http.StatusNotModified {
			t.Errorf("%s: revalidation status = %d, want 304", path, status)
		}
		if revalidated != "" {
			t.Errorf("%s: 304 carried a %d-byte body", path, len(revalidated))
		}
		if len(body) == 0 {
			t.Errorf("%s: first response was empty", path)
		}
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
// belong to a build that no longer exists.
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
