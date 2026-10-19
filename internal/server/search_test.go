package server

import (
	"net/url"
	"strings"
	"testing"
)

func searchURL(base, ref, q, scope string) string {
	u := base + "/search/" + ref + "?q=" + url.QueryEscape(q)
	if scope != "" {
		u += "&path=" + url.QueryEscape(scope)
	}
	return u
}

// TestSearchPageFindsLinesAndLinksThem: a result is a line number, the line,
// and a link to the file view at that line — the anchor work is what makes the
// link land on the match rather than merely near it.
func TestSearchPageFindsLinesAndLinksThem(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{
		"a.txt":     "alpha\nneedle here\n",
		"sub/b.txt": "needle too\n",
	})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "needle", ""))
	if !strings.Contains(body, "2 matches") {
		t.Errorf("match count missing:\n%.400s", body)
	}
	if !strings.Contains(body, "in 2 files") {
		t.Errorf("file count missing")
	}
	// The match is emphasised inside the snippet, so the line is not contiguous
	// in the markup: assert the rendered form.
	if !strings.Contains(body, `<em class="hit">needle</em> here`) {
		t.Errorf("the matched line is not rendered:\n%.600s", body)
	}
	if !strings.Contains(body, `<em class="hit">needle</em> too`) {
		t.Error("the second file's matched line is not rendered")
	}
	if !strings.Contains(body, `href="/blob/HEAD/a.txt#L2"`) {
		t.Errorf("result does not link to the first matching line:\n%.600s", body)
	}
	if !strings.Contains(body, `href="/blob/HEAD/sub/b.txt#L1"`) {
		t.Error("result for a nested file does not link to its line")
	}
}

// TestSearchReadsCommittedBytesForModifiedFiles, at the HTTP level: the blob
// page shows a modified file's committed content, so the search must find that
// content and not what the worktree currently holds. This is the property that
// keeps a result's line number pointing at the line it names.
func TestSearchReadsCommittedBytesForModifiedFiles(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"file.txt": "v1\nneedle\n"})
	ts, _ := newTestServer(t, dir)

	writeWorktreeFile(t, dir, "file.txt", "v1\nchanged\n")

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "needle", ""))
	if !strings.Contains(body, "needle") {
		t.Error("committed content was not found")
	}
	empty, _ := getBody(t, searchURL(ts.URL, "HEAD", "changed", ""))
	if !strings.Contains(empty, "No matches") {
		t.Error("search found worktree-only content in a tracked file")
	}
	// And the file view agrees: it renders the committed bytes too.
	blob, _ := getBody(t, ts.URL+"/blob/HEAD/file.txt")
	if !strings.Contains(blob, "needle") {
		t.Error("the file view does not show the committed line the search found")
	}
}

// TestSearchMarksMatchesThatAreNotCommitted: a file only the worktree has is
// findable, and says so.
func TestSearchMarksMatchesThatAreNotCommitted(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"a.txt": "alpha\n"})
	ts, _ := newTestServer(t, dir)
	writeWorktreeFile(t, dir, "fresh.txt", "fresh needle\n")

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "fresh needle", ""))
	if !strings.Contains(body, `class="badge b-untracked"`) {
		t.Errorf("an uncommitted match is not badged:\n%.600s", body)
	}
	if !strings.Contains(body, "fresh.txt") {
		t.Error("the untracked file was not found")
	}
}

// TestSearchScopedEmptyStateOffersTheWholeRepository is the honesty requirement:
// a scope that finds nothing must say so *and* offer the wider search, which
// must then actually find it.
func TestSearchScopedEmptyStateOffersTheWholeRepository(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"sub/b.txt": "deep-needle\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "deep-needle", "internal"))
	if !strings.Contains(body, "No matches") {
		t.Fatalf("a scoped search that found nothing did not say so:\n%.400s", body)
	}
	if !strings.Contains(body, "under <code>internal</code>") {
		t.Error("the empty state does not state the scope it searched")
	}
	want := `href="/search/HEAD?q=deep-needle"`
	if !strings.Contains(body, want) {
		t.Fatalf("the empty state does not offer the whole repository:\n%.600s", body)
	}
	whole, _ := getBody(t, ts.URL+"/search/HEAD?q=deep-needle")
	if !strings.Contains(whole, "deep-needle") {
		t.Error("the offered whole-repository search does not find the match")
	}
}

// TestSearchStatesItsScopeInTheResults: the results header names the scope, not
// just the count.
func TestSearchStatesItsScopeInTheResults(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"sub/b.txt": "scoped-needle\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "scoped-needle", "sub"))
	if !strings.Contains(body, "under <code>sub</code>") {
		t.Errorf("results do not state their scope:\n%.400s", body)
	}
	whole, _ := getBody(t, searchURL(ts.URL, "HEAD", "scoped-needle", ""))
	if !strings.Contains(whole, "the whole repository") {
		t.Error("an unscoped search does not say what it searched")
	}
}

// TestSearchEscapesQueryAndContent: both halves of a result row are input a
// stranger can influence — the query typed, and the line found in a file.
func TestSearchEscapesQueryAndContent(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{
		"hostile.html": `<img src=x onerror="alert(1)">needle</img>`,
	})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "needle", ""))
	if strings.Contains(body, "<img src=x") {
		t.Error("file content reached the page unescaped")
	}
	if !strings.Contains(body, "&lt;img") {
		t.Error("escaped file content is not present at all")
	}

	queryBody, _ := getBody(t, searchURL(ts.URL, "HEAD", `"><script>alert(1)</script>`, ""))
	if strings.Contains(queryBody, "<script>alert(1)") {
		t.Error("the query reached the page unescaped")
	}
	if !strings.Contains(queryBody, "&lt;script&gt;") {
		t.Error("the escaped query is not present at all")
	}
}

func TestSearchCapsAndSaysSo(t *testing.T) {
	dir := initTestRepo(t)
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("needle here\n")
	}
	commitFiles(t, dir, "one", map[string]string{"many.txt": b.String()})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, searchURL(ts.URL, "HEAD", "needle", ""))
	if !strings.Contains(body, "stopped at 200") {
		t.Errorf("a capped search does not say it was capped:\n%.400s", body)
	}
}

func TestSearchPageWithoutQueryExplainsItself(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"a.txt": "alpha\n"})
	ts, _ := newTestServer(t, dir)

	for _, path := range []string{"/search/HEAD", "/search/HEAD?q="} {
		body, _ := getBody(t, ts.URL+path)
		if !strings.Contains(body, "Searching") {
			t.Errorf("%s: no guidance for an empty query", path)
		}
	}
}

func TestSearchUnknownRefIs404(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"a.txt": "alpha\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/search/nope?q=x")
	if !strings.Contains(body, "unknown ref") {
		t.Errorf("expected an unknown-ref page, got:\n%.200s", body)
	}
}

// TestHeaderSearchBoxFollowsContext: the box in the header carries the path you
// are looking at as its scope, and the results page carries the scope it just
// searched. Either way the scope is visible in the markup rather than implicit.
func TestHeaderSearchBoxFollowsContext(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"sub/b.txt": "alpha\n"})
	ts, _ := newTestServer(t, dir)

	tree, _ := getBody(t, ts.URL+"/tree/HEAD/sub/")
	if !strings.Contains(tree, `class="searchbox"`) {
		t.Fatal("the header has no search box")
	}
	if !strings.Contains(tree, `<input type="hidden" name="path" value="sub">`) {
		t.Error("the header box does not carry the directory you are in")
	}
	if !strings.Contains(tree, `href="/search/HEAD"`) {
		t.Error("the narrow-width search link is missing")
	}

	results, _ := getBody(t, searchURL(ts.URL, "HEAD", "alpha", "sub"))
	if !strings.Contains(results, `value="alpha"`) {
		t.Error("the box does not carry the query on the results page")
	}
	if !strings.Contains(results, `<input type="hidden" name="path" value="sub">`) {
		t.Error("the box does not carry the scope on the results page")
	}

	root, _ := getBody(t, ts.URL+"/tree/HEAD/")
	if strings.Contains(root, `name="path"`) {
		t.Error("the root tree page should not scope the box to anything")
	}
}
