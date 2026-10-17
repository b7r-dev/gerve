package git

import (
	"context"
	"fmt"
	"strings"
)

// LogPage is one page of commit history.
type LogPage struct {
	Commits []CommitInfo
	Next    bool
}

// Log returns a page of history for refHash, optionally scoped to a path.
// follow (log --follow) applies only to single files.
func (r *Repo) Log(ctx context.Context, refHash string, page, per int, path string, follow bool) (*LogPage, error) {
	args := []string{
		"log", "--no-color", "--format=" + recFmt,
		fmt.Sprintf("--max-count=%d", per+1),
		fmt.Sprintf("--skip=%d", page*per),
	}
	if follow {
		args = append(args, "--follow")
	}
	args = append(args, refHash)
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		if isEmptyHistoryErr(err) {
			return &LogPage{}, nil
		}
		return nil, err
	}
	commits := parseLogRecords(out)
	p := &LogPage{Commits: commits}
	if len(p.Commits) > per {
		p.Commits = p.Commits[:per]
		p.Next = true
	}
	return p, nil
}

func isEmptyHistoryErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "does not have any commits yet") ||
		strings.Contains(msg, "unknown revision") ||
		strings.Contains(msg, "bad revision")
}

func parseLogRecords(out []byte) []CommitInfo {
	recs := recordChunks(out)
	commits := make([]CommitInfo, 0, len(recs))
	for _, rec := range recs {
		if len(rec) == 0 {
			continue
		}
		if info, ok := parseCommitRecord(rec[0]); ok {
			commits = append(commits, info)
		}
	}
	return commits
}

// IsFileAt reports whether path is a blob (not a tree) at the given commit.
func (r *Repo) IsFileAt(refHash, path string) bool {
	if path == "" {
		return false
	}
	b, err := r.gitBlob(refHash, path)
	return err == nil && b != nil
}
