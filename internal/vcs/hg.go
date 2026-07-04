package vcs

import (
	"context"
	"strconv"
	"strings"
)

// Mercurial implements VCS for Mercurial (hg) repositories.
type Mercurial struct{}

func (Mercurial) Name() string        { return "hg" }
func (Mercurial) DisplayName() string { return "Mercurial" }

func (Mercurial) Status(ctx context.Context, dir string) (string, error) {
	return runVCSCmd(ctx, dir, "hg", "status")
}

func (Mercurial) Diff(ctx context.Context, dir string, cached bool, file string) (string, error) {
	// hg doesn't have a staging area like git's index; cached is ignored.
	args := []string{"diff"}
	if file != "" {
		args = append(args, "--", file)
	}
	return runVCSCmd(ctx, dir, "hg", args...)
}

func (Mercurial) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}
	return runVCSCmd(ctx, dir, "hg", "log", "-l", strconv.Itoa(count), "--template", "{rev}:{node|short} {desc|firstline}\n")
}

func (Mercurial) Add(ctx context.Context, dir string, files []string) (string, error) {
	args := []string{"add", "--"}
	args = append(args, files...)
	return runVCSCmd(ctx, dir, "hg", args...)
}

func (Mercurial) Commit(ctx context.Context, dir string, message string) (string, error) {
	return runVCSCmd(ctx, dir, "hg", "commit", "-m", message)
}

func (Mercurial) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := runVCSCmd(ctx, dir, "hg", "branch")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (Mercurial) IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := runVCSCmd(ctx, dir, "hg", "status")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// Ensure Mercurial satisfies VCS at compile time.
var _ VCS = Mercurial{}
