package git

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FileDiff is one file's change in a commit.
type FileDiff struct {
	Status string // "A", "M", "D", "R", "C", "T"
	Old    string // previous path ("" for new files)
	New    string // current path ("" for deleted files)
	Binary bool
	Adds   int
	Dels   int
	Patch  string // unified patch text for this file
}

// CommitDiff is every file changed by a commit.
type CommitDiff struct {
	Files []FileDiff
}

// CommitDiff returns the patch of a commit. parent is empty for normal
// commits (diff against the sole parent); for merges pass the parent to
// diff against, usually the first. The patch always reads parent→commit:
// git diff-tree A B diffs A into B, so the parent goes first.
func (r *Repo) CommitDiff(ctx context.Context, hash, parent string) (*CommitDiff, error) {
	args := []string{"diff-tree", "-r", "--no-color", "-p", "-M", "--no-ext-diff"}
	if parent == "" {
		args = append(args, "--root", hash)
	} else {
		args = append(args, parent, hash)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	return ParsePatch(out), nil
}

func ParsePatch(out []byte) *CommitDiff {
	d := &CommitDiff{}
	var cur *FileDiff
	var buf strings.Builder
	inHunk := false

	flush := func() {
		if cur != nil {
			cur.Patch = buf.String()
		}
		buf.Reset()
	}

	for _, ln := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			flush()
			old, new := parseDiffGitLine(ln)
			cur = &FileDiff{Status: "M", Old: old, New: new}
			d.Files = append(d.Files, *cur)
			cur = &d.Files[len(d.Files)-1]
			inHunk = false
		case cur == nil:
			continue
		case strings.HasPrefix(ln, "new file mode"):
			cur.Status, cur.Old = "A", ""
		case strings.HasPrefix(ln, "deleted file mode"):
			cur.Status, cur.New = "D", ""
		case strings.HasPrefix(ln, "rename from "):
			cur.Status, cur.Old = "R", unquoteMaybe(strings.TrimPrefix(ln, "rename from "))
		case strings.HasPrefix(ln, "rename to "):
			cur.Status = "R"
			cur.New = unquoteMaybe(strings.TrimPrefix(ln, "rename to "))
		case strings.HasPrefix(ln, "copy from "):
			cur.Status, cur.Old = "C", unquoteMaybe(strings.TrimPrefix(ln, "copy from "))
		case strings.HasPrefix(ln, "copy to "):
			cur.Status = "C"
			cur.New = unquoteMaybe(strings.TrimPrefix(ln, "copy to "))
		case strings.HasPrefix(ln, "Binary files "), ln == "GIT binary patch":
			cur.Binary = true
		case strings.HasPrefix(ln, "--- "):
			if p := stripDiffPath(ln[4:]); p != "" {
				cur.Old = p
			}
		case strings.HasPrefix(ln, "+++ "):
			if p := stripDiffPath(ln[4:]); p != "" {
				cur.New = p
			}
		case strings.HasPrefix(ln, "@@"):
			inHunk = true
		default:
			if inHunk && !cur.Binary && len(ln) > 0 {
				switch ln[0] {
				case '+':
					cur.Adds++
				case '-':
					cur.Dels++
				}
			}
		}
		buf.WriteString(ln)
		buf.WriteByte('\n')
	}
	flush()
	return d
}

func parseDiffGitLine(ln string) (old, new string) {
	rest := unquoteMaybe(strings.TrimPrefix(ln, "diff --git "))
	i := strings.LastIndex(rest, " b/")
	if i < 0 {
		if j := strings.Index(rest, " "); j >= 0 {
			return strings.TrimPrefix(rest[:j], "a/"), strings.TrimPrefix(rest[j+1:], "b/")
		}
		return "", ""
	}
	return strings.TrimPrefix(rest[:i], "a/"), strings.TrimPrefix(rest[i+3:], "b/")
}

// stripDiffPath handles "--- a/x", "+++ b/y", "--- /dev/null".
func stripDiffPath(s string) string {
	s = unquoteMaybe(strings.TrimSpace(s))
	switch {
	case s == "/dev/null":
		return ""
	case strings.HasPrefix(s, "a/"):
		return s[2:]
	case strings.HasPrefix(s, "b/"):
		return s[2:]
	default:
		return s
	}
}

func unquoteMaybe(s string) string {
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// --- unified rows and side-by-side pairing ----------------------------------

// Row is one line of a unified diff table.
type Row struct {
	Kind byte // ' ', '+', '-'
	Old  int  // line number in the old file, 0 when N/A
	New  int  // line number in the new file, 0 when N/A
	Text string
}

// UnifiedRows converts a single file's patch into numbered rows.
func UnifiedRows(patch string) []Row {
	var rows []Row
	oldNo, newNo := 0, 0
	inHunk := false
	for _, ln := range strings.Split(patch, "\n") {
		if strings.HasPrefix(ln, "@@") {
			if m := hunkRe.FindStringSubmatch(ln); m != nil {
				oldNo, _ = strconv.Atoi(m[1])
				newNo, _ = strconv.Atoi(m[2])
			}
			inHunk = true
			continue
		}
		if !inHunk || strings.HasPrefix(ln, "---") || strings.HasPrefix(ln, "+++") {
			continue
		}
		if ln == "" || ln == "\\ No newline at end of file" {
			continue
		}
		kind, text := byte(' '), ""
		if len(ln) > 0 {
			kind, text = ln[0], ln[1:]
		}
		row := Row{Kind: kind, Text: text}
		switch kind {
		case '-':
			row.Old = oldNo
			oldNo++
		case '+':
			row.New = newNo
			newNo++
		default:
			row.Old, row.New = oldNo, newNo
			oldNo++
			newNo++
		}
		rows = append(rows, row)
	}
	return rows
}

// Cell is one half of a side-by-side row; Kind 0 marks an empty gutter cell.
type Cell struct {
	Kind byte
	Num  int
	Text string
}

// SideHunk is one hunk rendered as paired left/right line lists.
type SideHunk struct {
	Header string
	Left   []Cell
	Right  []Cell
}

var hunkRe = regexp.MustCompile(`^@@+ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// SideBySide converts a single file's patch into aligned side-by-side hunks.
// Runs of deletions and additions are zipped together; the shorter side is
// padded with empty cells.
func SideBySide(patch string) []SideHunk {
	var hunks []SideHunk
	var cur *SideHunk
	var lrun, rrun []Cell
	oldNo, newNo := 0, 0
	inHunk := false

	flush := func() {
		if cur == nil {
			lrun, rrun = nil, nil
			return
		}
		n := len(lrun)
		if len(rrun) > n {
			n = len(rrun)
		}
		for i := 0; i < n; i++ {
			var l, r Cell
			if i < len(lrun) {
				l = lrun[i]
			}
			if i < len(rrun) {
				r = rrun[i]
			}
			cur.Left = append(cur.Left, l)
			cur.Right = append(cur.Right, r)
		}
		lrun, rrun = nil, nil
	}

	for _, ln := range strings.Split(patch, "\n") {
		if strings.HasPrefix(ln, "@@") {
			flush()
			if m := hunkRe.FindStringSubmatch(ln); m != nil {
				oldNo, _ = strconv.Atoi(m[1])
				newNo, _ = strconv.Atoi(m[2])
			}
			hunks = append(hunks, SideHunk{Header: strings.TrimSpace(ln)})
			cur = &hunks[len(hunks)-1]
			inHunk = true
			continue
		}
		if !inHunk || cur == nil {
			continue
		}
		if strings.HasPrefix(ln, "---") || strings.HasPrefix(ln, "+++") {
			continue
		}
		if ln == "" || ln == "\\ No newline at end of file" {
			continue
		}
		kind, text := byte(' '), ""
		if len(ln) > 0 {
			kind, text = ln[0], ln[1:]
		}
		switch kind {
		case '-':
			if len(rrun) > 0 {
				flush()
			}
			lrun = append(lrun, Cell{'-', oldNo, text})
			oldNo++
		case '+':
			rrun = append(rrun, Cell{'+', newNo, text})
			newNo++
		default:
			flush()
			cur.Left = append(cur.Left, Cell{' ', oldNo, text})
			cur.Right = append(cur.Right, Cell{' ', newNo, text})
			oldNo++
			newNo++
		}
	}
	flush()
	return hunks
}

// --- blame ------------------------------------------------------------------

// BlameLine is one numbered source line.
type BlameLine struct {
	Num  int
	Text string
}

// BlameGroup is a run of lines last touched by the same commit.
type BlameGroup struct {
	Commit   CommitInfo
	Boundary bool
	Lines    []BlameLine
}

// Blame returns blame groups for a path at the given commit.
func (r *Repo) Blame(ctx context.Context, refHash, path string) ([]BlameGroup, error) {
	out, err := r.git(ctx, "blame", "--porcelain", "-w", refHash, "--", path)
	if err != nil {
		return nil, err
	}
	return parseBlame(out), nil
}

func parseBlame(out []byte) []BlameGroup {
	var groups []*BlameGroup
	var cur *BlameGroup
	var num int
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "\t") {
			if cur != nil {
				num++
				cur.Lines = append(cur.Lines, BlameLine{Num: num, Text: ln[1:]})
			}
			continue
		}
		if ln == "" {
			continue
		}
		f := strings.Fields(ln)
		if len(f) >= 4 && isFullHex(f[0]) {
			cur = &BlameGroup{
				Commit:   CommitInfo{Hash: f[0], Short: shortHash(f[0])},
				Boundary: len(f) >= 5 && f[4] == "boundary",
			}
			groups = append(groups, cur)
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(ln, "author "):
			cur.Commit.Author = strings.TrimPrefix(ln, "author ")
		case strings.HasPrefix(ln, "author-time "):
			if sec, err := parseInt64(strings.TrimPrefix(ln, "author-time ")); err == nil {
				cur.Commit.Time = time.Unix(sec, 0)
			}
		case strings.HasPrefix(ln, "summary "):
			cur.Commit.Subject = strings.TrimPrefix(ln, "summary ")
		}
	}
	out2 := make([]BlameGroup, 0, len(groups))
	for _, g := range groups {
		out2 = append(out2, *g)
	}
	return out2
}

func isFullHex(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
