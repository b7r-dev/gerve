package git

import (
	"strings"
	"testing"
	"time"
)

func TestParsePatchRenameAndCounts(t *testing.T) {
	sample := strings.Join([]string{
		`diff --git a/old.go b/new.go`,
		`similarity index 91%`,
		`rename from old.go`,
		`rename to new.go`,
		`--- a/old.go`,
		`+++ b/new.go`,
		`@@ -1,4 +1,5 @@`,
		` package main`,
		` `,
		`+import "fmt"`,
		` func main() {`,
		`-	fmt.Println("old")`,
		`+	fmt.Println("new")`,
		` }`,
		`diff --git a/logo.png b/logo.png`,
		`index 1234..5678 100644`,
		`Binary files a/logo.png and b/logo.png differ`,
		`diff --git a/gone.txt b/gone.txt`,
		`deleted file mode 100644`,
		`--- a/gone.txt`,
		`+++ /dev/null`,
		`@@ -1 +0,0 @@`,
		`-bye`,
		``, // trailing newline artifact
	}, "\n")

	d := ParsePatch([]byte(sample))
	if len(d.Files) != 3 {
		t.Fatalf("want 3 files, got %d: %+v", len(d.Files), d.Files)
	}

	r := d.Files[0]
	if r.Status != "R" || r.Old != "old.go" || r.New != "new.go" {
		t.Errorf("rename file: got status=%s old=%q new=%q", r.Status, r.Old, r.New)
	}
	if r.Adds != 2 || r.Dels != 1 {
		t.Errorf("rename counts: adds=%d dels=%d", r.Adds, r.Dels)
	}
	if !strings.Contains(r.Patch, `+import "fmt"`) {
		t.Errorf("patch text missing content")
	}

	b := d.Files[1]
	if !b.Binary || b.Status != "M" || b.Adds+b.Dels != 0 {
		t.Errorf("binary file: %+v", b)
	}

	g := d.Files[2]
	if g.Status != "D" || g.Old != "gone.txt" || g.New != "" || g.Dels != 1 {
		t.Errorf("deleted file: %+v", g)
	}
}

func TestSideBySidePairing(t *testing.T) {
	patch := strings.Join([]string{
		`@@ -1,4 +1,4 @@`,
		` a`,
		`-b`,
		`-c`,
		`+B`,
		`+C`,
		` d`,
		`+e`,
		``, // trailing
	}, "\n")

	hunks := SideBySide(patch)
	if len(hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if len(h.Left) != len(h.Right) {
		t.Fatalf("left/right length mismatch: %d vs %d", len(h.Left), len(h.Right))
	}
	type want struct{ lk, rk byte; ln, rn int }
	wantRows := []want{
		{' ', ' ', 1, 1}, // context a
		{'-', '+', 2, 2}, // b -> B
		{'-', '+', 3, 3}, // c -> C
		{' ', ' ', 4, 4}, // context d
		{0, '+', 0, 5},   // inserted e: empty left
	}
	for i, w := range wantRows {
		if h.Left[i].Kind != w.lk || h.Right[i].Kind != w.rk || h.Left[i].Num != w.ln || h.Right[i].Num != w.rn {
			t.Errorf("row %d: got L(%q,%d) R(%q,%d), want L(%q,%d) R(%q,%d)",
				i, string(h.Left[i].Kind), h.Left[i].Num, string(h.Right[i].Kind), h.Right[i].Num, string(w.lk), w.ln, string(w.rk), w.rn)
		}
	}
}

func TestUnifiedRows(t *testing.T) {
	patch := "@@ -1,2 +1,3 @@\n a\n-b\n+B\n+c\n"
	rows := UnifiedRows(patch)
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rows))
	}
	if rows[1].Kind != '-' || rows[1].Old != 2 || rows[1].New != 0 {
		t.Errorf("row 1: %+v", rows[1])
	}
	if rows[2].Kind != '+' || rows[2].Old != 0 || rows[2].New != 2 {
		t.Errorf("row 2: %+v", rows[2])
	}
	if rows[3].Kind != '+' || rows[3].Old != 0 || rows[3].New != 3 {
		t.Errorf("row 3: %+v", rows[3])
	}
}

func TestParseStatus(t *testing.T) {
	out := []byte("?? new.txt\x00 M mod.txt\x00R  renamed.txt\x00old-name.txt\x00D  gone.txt\x00A  staged.txt\x00")
	m := parseStatus(out)
	checks := map[string]ChangeKind{
		"new.txt":      Untracked,
		"mod.txt":      Modified,
		"renamed.txt":  Renamed,
		"old-name.txt": Deleted,
		"gone.txt":     Deleted,
		"staged.txt":   Added,
	}
	if len(m) != len(checks) {
		t.Fatalf("want %d entries, got %d: %+v", len(checks), len(m), m)
	}
	for p, k := range checks {
		if m[p].Kind != k {
			t.Errorf("%s: want %q got %q", p, k, m[p].Kind)
		}
	}
}

func TestParseCommitRecord(t *testing.T) {
	rec := "abc123def4567890123456789012345678901234\x1fabc1234\x1fJane Doe\x1f1700000000\x1ffix: the thing"
	info, ok := parseCommitRecord(rec)
	if !ok {
		t.Fatal("parse failed")
	}
	if info.Short != "abc123d" || info.Author != "Jane Doe" || info.Subject != "fix: the thing" {
		t.Errorf("got %+v", info)
	}
	if info.Time.Unix() != 1700000000 || info.Time.Equal(time.Time{}) {
		t.Errorf("time: %v", info.Time)
	}
}
