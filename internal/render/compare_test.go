package render

import (
	"bytes"
	"strings"
	"testing"
)

// TestComparePageNeverLinksAnEmptyMergeBase: the header's link is built from
// From, which the handler always sets. A page built without it must not produce
// /commit/ with an empty hash — the dead link this whole change exists to stop.
func TestComparePageNeverLinksAnEmptyMergeBase(t *testing.T) {
	rd, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := fixtureBase()
	var out bytes.Buffer
	if err := rd.pages["compare"].ExecuteTemplate(&out, "layout", ComparePage{Base: b}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `href="/commit/"`) {
		t.Errorf("an empty merge-base link was rendered:\n%.600s", out.String())
	}
	if !strings.Contains(out.String(), "merge base unavailable") {
		t.Error("the page does not say the merge base is missing")
	}

	out.Reset()
	c := commit()
	if err := rd.pages["compare"].ExecuteTemplate(&out, "layout", ComparePage{Base: b, From: c.Hash}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `href="/commit/`+c.Hash+`"`) {
		t.Errorf("the merge base is not linked:\n%.600s", out.String())
	}
}
