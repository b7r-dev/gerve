package git

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Bounds for one comparison. They are exported the way SearchLimit is so the
// page can state the limits it ran under instead of implying its answer is
// complete.
const (
	CompareMaxFiles    = 300
	CompareMaxCommits  = 100
	ComparePatchBudget = 8 << 20 // total bytes of patch text rendered
	CompareFileCap     = 1 << 20 // bytes of patch text for one file
	compareChunk       = 50      // paths per `git diff` invocation
)

// RangeFile is one file's change between two commits.
type RangeFile struct {
	Status     string // "A", "M", "D", "R", "C", "T"
	Old, New   string
	Adds, Dels int
	Binary     bool
}

// Range is what changed between two commits, computed in two passes: a stat
// pass that decides what can be shown, then patches for the files that survived
// the caps. The diff is against the merge base, not the base tip — three-dot
// semantics, the same thing a pull request's "changes" view shows. Two-dot would
// present the base branch's own commits as reversed changes.
type Range struct {
	Base, Head CommitInfo
	// From is the commit the diff was taken against: the merge base, or the
	// base itself when the histories are unrelated.
	From          string
	MergeBase     *CommitInfo
	Ahead, Behind int
	Commits       []CommitInfo
	CommitsMore   bool
	Files         []RangeFile
	FilesMore     int
	Adds, Dels    int
	Stopped       bool // the deadline cut the answer short
}

// Compare reports what changed between two resolved commits, optionally scoped
// to one path. It never renders a patch — that is RangePatch, once the caller
// knows which files survived the caps.
func (r *Repo) Compare(ctx context.Context, baseHash, headHash, scope string) (*Range, error) {
	rng := &Range{From: baseHash}
	if b, err := r.Commit(baseHash); err == nil {
		rng.Base = b.CommitInfo
	}
	if h, err := r.Commit(headHash); err == nil {
		rng.Head = h.CommitInfo
	}
	if baseHash == headHash {
		rng.MergeBase = &rng.Base
		return rng, nil
	}

	// The merge base is what makes this a review diff rather than a tree diff.
	// No common ancestor is an answer, not a failure: the diff degrades to
	// tree-to-tree and the page says so.
	if out, err := r.git(ctx, "merge-base", baseHash, headHash); err == nil {
		if mb := strings.TrimSpace(string(out)); isFullHex(mb) {
			rng.From = mb
			if m, err := r.Commit(mb); err == nil {
				rng.MergeBase = &m.CommitInfo
			}
		}
	} else if ctx.Err() != nil {
		rng.Stopped = true
		return rng, nil
	}

	if out, err := r.git(ctx, "rev-list", "--left-right", "--count", baseHash+"..."+headHash); err == nil {
		if f := strings.Fields(string(out)); len(f) == 2 {
			rng.Behind, _ = strconv.Atoi(f[0]) // commits only the base has
			rng.Ahead, _ = strconv.Atoi(f[1])  // commits only the head has
		}
	}

	// The commit list answers "what am I about to merge", so it is the branch's
	// history and deliberately not scoped by path.
	if out, err := r.git(ctx,
		"log", "--no-color", "--format="+recFmt,
		fmt.Sprintf("--max-count=%d", CompareMaxCommits+1),
		rng.From+".."+headHash); err == nil {
		commits := parseLogRecords(out)
		if len(commits) > CompareMaxCommits {
			commits = commits[:CompareMaxCommits]
			rng.CommitsMore = true
		}
		rng.Commits = commits
	} else if ctx.Err() != nil {
		rng.Stopped = true
		return rng, nil
	}

	// Stat pass: name-status carries the status and the rename pair, numstat
	// carries the counts. Both are tree-only; neither reads patch text.
	// GIT_NO_LAZY_FETCH matters: -M reads blobs to score similarity, and in a
	// partial clone that would fetch from the promisor remote — egress, which
	// gerve promises never happens. Degrading rename detection beats fetching.
	statArgs := func(mode string) []string {
		args := []string{"diff", "--no-color", "--no-ext-diff", "-M", mode, "-z", rng.From, headHash}
		if scope != "" {
			args = append(args, "--", scope)
		}
		return args
	}
	nsOut, err := r.gitEnv(ctx, noLazyFetch(), statArgs("--name-status")...)
	if err != nil {
		if ctx.Err() != nil {
			rng.Stopped = true
			return rng, nil
		}
		return nil, err
	}
	numOut, err := r.gitEnv(ctx, noLazyFetch(), statArgs("--numstat")...)
	if err != nil {
		if ctx.Err() != nil {
			rng.Stopped = true
			return rng, nil
		}
		return nil, err
	}

	counts := map[string]numstatRec{}
	for _, n := range parseNumstatZ(numOut) {
		counts[numstatKey(n)] = n
		if !n.binary {
			rng.Adds += n.adds
			rng.Dels += n.dels
		}
	}
	for _, s := range parseNameStatusZ(nsOut) {
		key := rangeKey(s.old, s.new, s.status)
		n := counts[key]
		rng.Files = append(rng.Files, RangeFile{
			Status: s.status,
			Old:    s.old,
			New:    s.new,
			Adds:   n.adds,
			Dels:   n.dels,
			Binary: n.binary,
		})
	}
	if len(rng.Files) > CompareMaxFiles {
		rng.FilesMore = len(rng.Files) - CompareMaxFiles
		rng.Files = rng.Files[:CompareMaxFiles]
	}
	return rng, nil
}

// RangePatches is the rendered half of a comparison, keyed by rangeKey.
type RangePatches struct {
	ByKey     map[string]FileDiff
	Truncated bool // a cap or the byte budget left files unrendered
	Stopped   bool // the deadline cut the patch pass short
}

// RangePatch fetches patch text for the given files only, in chunks, inside a
// byte budget. A file without an entry in ByKey is simply not rendered — the
// page marks it rather than showing an empty card.
func (r *Repo) RangePatch(ctx context.Context, from, head string, files []RangeFile) RangePatches {
	res := RangePatches{ByKey: make(map[string]FileDiff, len(files))}
	budget := ComparePatchBudget

	for i := 0; i < len(files); i += compareChunk {
		if budget <= 0 {
			res.Truncated = true
			break
		}
		end := i + compareChunk
		if end > len(files) {
			end = len(files)
		}
		args := []string{"diff", "--no-color", "--no-ext-diff", "-M", from, head, "--"}
		added := 0
		for _, f := range files[i:end] {
			// A rename contributes both sides, so detection works inside the
			// chunk's pathspec instead of degrading to add+delete.
			switch {
			case f.Status == "R" || f.Status == "C":
				args = append(args, f.Old, f.New)
				added += 2
			case f.New != "":
				args = append(args, f.New)
				added++
			default:
				args = append(args, f.Old)
				added++
			}
		}
		if added == 0 {
			continue
		}
		out, err := r.gitEnv(ctx, noLazyFetch(), args...)
		if err != nil {
			if ctx.Err() != nil {
				res.Stopped = true
				return res
			}
			res.Truncated = true
			continue
		}
		for _, d := range ParsePatch(out).Files {
			if len(d.Patch) > CompareFileCap || budget-len(d.Patch) < 0 {
				res.Truncated = true
				continue
			}
			budget -= len(d.Patch)
			res.ByKey[rangeKey(d.Old, d.New, d.Status)] = d
		}
	}
	return res
}

// noLazyFetch stops git from fetching missing objects in a partial clone. gerve
// is a read-only, zero-egress viewer; a diff that quietly reaches for the
// promisor remote would break that promise to make a page look complete.
func noLazyFetch() []string { return []string{"GIT_NO_LAZY_FETCH=1"} }

// --- parsing ----------------------------------------------------------------

type statusRec struct{ status, old, new string }

func parseNameStatusZ(out []byte) []statusRec {
	toks := bytes.Split(out, []byte{0})
	var recs []statusRec
	for i := 0; i < len(toks); i++ {
		s := string(toks[i])
		if s == "" {
			continue
		}
		switch s[0] {
		case 'R', 'C':
			if i+2 >= len(toks) {
				return recs
			}
			recs = append(recs, statusRec{status: s[:1], old: string(toks[i+1]), new: string(toks[i+2])})
			i += 2
		case 'A', 'D', 'M', 'T', 'U', 'X', 'B':
			if i+1 >= len(toks) {
				return recs
			}
			p := string(toks[i+1])
			rec := statusRec{status: s[:1]}
			if s[0] == 'D' {
				rec.old = p
			} else {
				rec.new = p
			}
			recs = append(recs, rec)
			i++
		}
	}
	return recs
}

type numstatRec struct {
	adds, dels int
	binary     bool
	old, new   string
}

func parseNumstatZ(out []byte) []numstatRec {
	toks := bytes.Split(out, []byte{0})
	var recs []numstatRec
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if len(t) == 0 {
			continue
		}
		// adds \t dels \t path — SplitN keeps tabs inside a pathname.
		f := bytes.SplitN(t, []byte{'\t'}, 3)
		if len(f) < 3 {
			continue
		}
		rec := numstatRec{binary: string(f[0]) == "-"}
		if !rec.binary {
			rec.adds, _ = strconv.Atoi(string(f[0]))
			rec.dels, _ = strconv.Atoi(string(f[1]))
		}
		if path := string(f[2]); path != "" {
			rec.new = path
		} else if i+2 < len(toks) {
			// A rename record has an empty third field, then the two paths.
			rec.old, rec.new = string(toks[i+1]), string(toks[i+2])
			i += 2
		}
		recs = append(recs, rec)
	}
	return recs
}

// numstatKey names a numstat record the way rangeKey names a name-status or
// patch record: a rename by its pair, everything else by its single path.
func numstatKey(n numstatRec) string {
	if n.old != "" {
		return n.old + "\x00" + n.new
	}
	return "\x00" + n.new
}

// rangeKey is the join between the stat pass and the patch pass. Both sides
// produce (old, new, status) in the same shape; a rename is the only case where
// the pair matters, and everything else keys on its single pathname.
func rangeKey(old, new, status string) string {
	if status == "R" || status == "C" {
		return old + "\x00" + new
	}
	if new != "" {
		return "\x00" + new
	}
	return "\x00" + old
}
