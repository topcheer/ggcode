package vcs

import (
	"context"
	"strconv"
	"strings"
)

// Jujutsu implements VCS for Jujutsu (jj) repositories.
// jj often coexists with git; Detect returns jj only when .jj exists without .git.
type Jujutsu struct{}

func (Jujutsu) Name() string        { return "jj" }
func (Jujutsu) DisplayName() string { return "Jujutsu" }

func (Jujutsu) Status(ctx context.Context, dir string) (string, error) {
	return runVCSCmd(ctx, dir, "jj", "st")
}

func (Jujutsu) Diff(ctx context.Context, dir string, cached bool, file string) (string, error) {
	args := []string{"diff"}
	if file != "" {
		args = append(args, file)
	}
	return runVCSCmd(ctx, dir, "jj", args...)
}

func (Jujutsu) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}
	return runVCSCmd(ctx, dir, "jj", "log", "-n", strconv.Itoa(count))
}

func (Jujutsu) Add(ctx context.Context, dir string, files []string) (string, error) {
	// jj automatically tracks new files, but we can explicitly file:add.
	args := []string{"file", "track"}
	args = append(args, files...)
	return runVCSCmd(ctx, dir, "jj", args...)
}

func (Jujutsu) Commit(ctx context.Context, dir string, message string) (string, error) {
	return runVCSCmd(ctx, dir, "jj", "describe", "-m", message)
}

func (Jujutsu) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := runVCSCmd(ctx, dir, "jj", "log", "-r", "@", "--no-graph", "-T", "bookmarks")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (Jujutsu) IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := runVCSCmd(ctx, dir, "jj", "st")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "" || strings.Contains(out, "The working copy is clean"), nil
}

// Ensure Jujutsu satisfies VCS at compile time.
var _ VCS = Jujutsu{}
