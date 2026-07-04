package vcs

import (
	"context"
	"strconv"
	"strings"
)

// Subversion implements VCS for Subversion (svn) working copies.
type Subversion struct{}

func (Subversion) Name() string        { return "svn" }
func (Subversion) DisplayName() string { return "Subversion" }

func (Subversion) Status(ctx context.Context, dir string) (string, error) {
	return runVCSCmd(ctx, dir, "svn", "status")
}

func (Subversion) Diff(ctx context.Context, dir string, cached bool, file string) (string, error) {
	// svn has no staging area; cached is ignored.
	args := []string{"diff"}
	if file != "" {
		args = append(args, file)
	}
	return runVCSCmd(ctx, dir, "svn", args...)
}

func (Subversion) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}
	// Use -r 1:HEAD with -l to get most recent commits. Plain `svn log`
	// in a working copy may not show recently committed revisions reliably.
	return runVCSCmd(ctx, dir, "svn", "log", "-r", "1:HEAD", "-l", strconv.Itoa(count))
}

func (Subversion) Add(ctx context.Context, dir string, files []string) (string, error) {
	args := []string{"add", "--"}
	args = append(args, files...)
	return runVCSCmd(ctx, dir, "svn", args...)
}

func (Subversion) Commit(ctx context.Context, dir string, message string) (string, error) {
	return runVCSCmd(ctx, dir, "svn", "commit", "-m", message, "--non-interactive")
}

func (Subversion) CurrentBranch(ctx context.Context, dir string) (string, error) {
	// svn doesn't have branches in the git sense; return the basename of the URL.
	out, err := runVCSCmd(ctx, dir, "svn", "info", "--show-item", "url")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)
	// Extract branch name from URL (e.g. .../branches/feature-x)
	parts := strings.Split(url, "/")
	for i, p := range parts {
		if p == "branches" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1], nil
	}
	return url, nil
}

func (Subversion) IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := runVCSCmd(ctx, dir, "svn", "status")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// Ensure Subversion satisfies VCS at compile time.
var _ VCS = Subversion{}
