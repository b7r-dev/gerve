package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	compareChunkBytes  = 4 << 20 // stdout cap for one `git diff` chunk
	compareChunk       = 50      // paths per `git diff` invocation
)

// compareStatBytes caps a stat listing's stdout. A listing is one short record
// per path, so a range big enough to blow this cannot be shown anyway — and
// without the cap the whole listing is buffered before CompareMaxFiles applies,
// which makes the page's memory bounded by the repository rather than by a
// constant. It is a var so a test can lower it instead of building that range.
var compareStatBytes = 8 << 20

// RangeFile is one file's change between two commits.
type RangeFile struct {
	Status     string // "A", "M", "D", "R", "C", "T"
	Old, New   string
	Adds, Dels int
	Binary     bool
}

// Key joins this file with its rendered patch in RangePatches.ByKey.
func (f RangeFile) Key() string { return rangeKey(f.Old, f.New, f.Status) }

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
	TooLarge      bool // the stat listing exceeded compareStatBytes
	// FilesIncomplete means the listing could not be read in full; the files
	// that did parse are shown, and the page must not call itself complete.
	FilesIncomplete bool
}

// ErrObjectsUnavailable means the comparison needs file contents that are not
// in this repository's object store — a partial clone, whose missing objects git
// would normally fetch. gerve never fetches, so it says so instead.
var ErrObjectsUnavailable = errors.New("objects are not available locally")

// Compare reports what changed between two resolved commits, optionally scoped
// to one path. It never renders a patch — that is RangePatch, once the caller
// knows which files survived the caps.
func (r *Repo) Compare(ctx context.Context, baseHash, headHash, scope string) (*Range, error) {
	rng := &Range{From: baseHash}
	if b, err := r.CommitCtx(ctx, baseHash); err == nil {
		rng.Base = b.CommitInfo
	}
	if h, err := r.CommitCtx(ctx, headHash); err == nil {
		rng.Head = h.CommitInfo
	}
	if baseHash == headHash {
		if rng.Base.Hash != "" {
			rng.MergeBase = &rng.Base
		}
		return rng, nil
	}

	// The merge base is what makes this a review diff rather than a tree diff.
	// No common ancestor is an answer, not a failure: the diff degrades to
	// tree-to-tree and the page says so. Only exit 1 means that; any other
	// failure must not be dressed up as an unrelated history, because that
	// silently serves the tree-to-tree diff this page exists to avoid.
	if out, err := r.git(ctx, "merge-base", baseHash, headHash); err == nil {
		if mb := strings.TrimSpace(string(out)); isFullHex(mb) {
			rng.From = mb
			if m, err := r.CommitCtx(ctx, mb); err == nil {
				rng.MergeBase = &m.CommitInfo
			}
		}
	} else if ctx.Err() != nil {
		rng.Stopped = true
		return rng, nil
	} else if code, ok := exitCode(err); !ok || code != 1 {
		return nil, r.statErr(err)
	}

	if out, err := r.git(ctx, "rev-list", "--left-right", "--count", baseHash+"..."+headHash); err == nil {
		if f := strings.Fields(string(out)); len(f) == 2 {
			rng.Behind, _ = strconv.Atoi(f[0]) // commits only the base has
			rng.Ahead, _ = strconv.Atoi(f[1])  // commits only the head has
		}
	} else if ctx.Err() != nil {
		rng.Stopped = true
		return rng, nil
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
		// --literal-pathspecs: a path beginning with ":" is a pathname here, not
		// pathspec magic. Without it, a file called ":notes.txt" silently drops
		// out of a comparison — and a path that looks like ":(exclude)*" can
		// empty a whole stat pass.
		args := []string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "-M", mode, "-z", rng.From, headHash}
		if scope != "" {
			args = append(args, "--", scope)
		}
		return args
	}
	nsOut, err := r.gitLimit(ctx, noLazyFetch(), compareStatBytes, statArgs("--name-status")...)
	if errors.Is(err, errOutputTooLarge) {
		// No file list is better than a list that pretends to be the whole
		// range: the page says the range is too large and offers a scope.
		rng.TooLarge = true
		return rng, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			rng.Stopped = true
			return rng, nil
		}
		return nil, r.statErr(err)
	}
	numOut, err := r.gitLimit(ctx, noLazyFetch(), compareStatBytes, statArgs("--numstat")...)
	if errors.Is(err, errOutputTooLarge) {
		rng.TooLarge = true
		return rng, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			rng.Stopped = true
			return rng, nil
		}
		return nil, r.statErr(err)
	}

	counts := map[string]numstatRec{}
	for _, n := range parseNumstatZ(numOut) {
		counts[numstatKey(n)] = n
		if !n.binary {
			rng.Adds += n.adds
			rng.Dels += n.dels
		}
	}
	statuses, complete := parseNameStatusZ(nsOut)
	if !complete {
		rng.FilesIncomplete = true
	}
	for _, s := range statuses {
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

// statErr keeps the honest answer when a stat pass fails: in a partial clone the
// contents git wants are simply not here, and gerve will not fetch them.
func (r *Repo) statErr(err error) error {
	if r.promisorPacks() {
		return fmt.Errorf("%w: %v", ErrObjectsUnavailable, err)
	}
	return err
}

// promisorPacks reports whether this clone keeps objects elsewhere and fetches
// them on demand. The pack marker survives the remote being removed, which the
// config does not — and a clone whose remote is gone is exactly the case gerve
// must not paper over.
func (r *Repo) promisorPacks() bool {
	base := filepath.Join(r.root, ".git")
	if !r.worktree {
		base = r.root
	}
	m, _ := filepath.Glob(filepath.Join(base, "objects", "pack", "*.promisor"))
	return len(m) > 0
}

// RangePatches is the rendered half of a comparison, keyed by rangeKey.
type RangePatches struct {
	ByKey       map[string]FileDiff
	Truncated   bool // a cap or the byte budget left files unrendered
	Stopped     bool // the deadline cut the patch pass short
	Unavailable bool // contents are not in the local object store (partial clone)
	Failed      bool // git could not read a chunk for a reason that is not size
}

// Get returns the patch for a file, tolerating a rename that the chunked pass
// paired differently from the stat pass: the card is still about the same path,
// and a missing patch only means "not rendered", never a wrong file.
func (p RangePatches) Get(f RangeFile) (FileDiff, bool) {
	if d, ok := p.ByKey[f.Key()]; ok {
		return d, true
	}
	for _, path := range []string{f.New, f.Old} {
		if path == "" {
			continue
		}
		if d, ok := p.ByKey["\x00"+path]; ok {
			return d, true
		}
	}
	return FileDiff{}, false
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
		// --literal-pathspecs for the same reason as the stat pass: a pathname
		// that begins with ":" is data, not magic.
		args := []string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "-M", from, head, "--"}
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
		// The chunk's stdout is capped, not just its rendered total: a chunk of
		// fifty large files can otherwise be hundreds of megabytes in memory
		// before CompareFileCap is ever consulted.
		limit := budget
		if limit > compareChunkBytes {
			limit = compareChunkBytes
		}
		out, err := r.gitLimit(ctx, noLazyFetch(), limit, args...)
		if err != nil {
			if ctx.Err() != nil {
				res.Stopped = true
				return res
			}
			if errors.Is(err, errOutputTooLarge) {
				res.Truncated = true
				continue
			}
			if r.promisorPacks() {
				res.Unavailable = true
			} else {
				// Not a size cap and not a partial clone: git refused the
				// read. The page may not blame the size of the change for it.
				res.Failed = true
			}
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

// parseNameStatusZ reads `--name-status -z`. The boolean says the listing was
// read to the end: a truncated or unrecognised record stops the parse, and the
// caller must not pass the prefix off as the whole range.
func parseNameStatusZ(out []byte) (recs []statusRec, complete bool) {
	toks := bytes.Split(out, []byte{0})
	for i := 0; i < len(toks); i++ {
		s := string(toks[i])
		if s == "" {
			continue
		}
		switch s[0] {
		case 'R', 'C':
			if i+2 >= len(toks) || len(toks[i+2]) == 0 {
				return recs, false
			}
			recs = append(recs, statusRec{status: s[:1], old: string(toks[i+1]), new: string(toks[i+2])})
			i += 2
		case 'A', 'D', 'M', 'T', 'U', 'X', 'B':
			if i+1 >= len(toks) || len(toks[i+1]) == 0 {
				return recs, false
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
		default:
			// Not a status git emits: the stream is out of step, and the next
			// token would be read as a status if we kept going.
			return recs, false
		}
	}
	return recs, true
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
