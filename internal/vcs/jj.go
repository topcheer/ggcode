package vcs

import (
	"context"
	"strconv"
	"strings"
)

// Jujutsu implements VCS for Jujutsu (jj) repositories.
// jj often coexists with git; Detect returns jj only when .jj exists without .git.
//
// jj's model differs from git: the working copy is always a commit (a "change").
// Files are tracked automatically — there's no staging area. "Committing" means
// describing the current change and then creating a new empty change on top.
type Jujutsu struct{}

func (Jujutsu) Name() string        { return "jj" }
func (Jujutsu) DisplayName() string { return "Jujutsu" }

func (Jujutsu) Status(ctx context.Context, dir string) (string, error) {
	return runVCSCmd(ctx, dir, "jj", "st")
}

func (Jujutsu) Diff(ctx context.Context, dir string, cached bool, file string) (string, error) {
	args := []string{"diff"}
	if file != "" {
		args = append(args, "--", file)
	}
	return runVCSCmd(ctx, dir, "jj", args...)
}

func (Jujutsu) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}
	// Use a compact template for readability.
	return runVCSCmd(ctx, dir, "jj", "log",
		"-n", strconv.Itoa(count),
		"--no-graph",
		"-T", `commit_id.short() ++ " " ++ description.first_line() ++ "\n"`)
}

func (Jujutsu) Add(ctx context.Context, dir string, files []string) (string, error) {
	// jj automatically tracks all new files in the working copy.
	// `jj file track` is rarely needed, but we call it for explicitness.
	// If the file is already tracked, jj exits 0 silently.
	args := []string{"file", "track", "--"}
	args = append(args, files...)
	return runVCSCmd(ctx, dir, "jj", args...)
}

func (Jujutsu) Commit(ctx context.Context, dir string, message string) (string, error) {
	// In jj, the working copy IS a commit. To "commit":
	// 1. `jj describe -m` sets the message on the current change
	// 2. `jj new` creates a new empty change on top (finalizing the previous one)
	describeOut, err := runVCSCmd(ctx, dir, "jj", "describe", "-m", message)
	if err != nil {
		return describeOut, err
	}
	newOut, err := runVCSCmd(ctx, dir, "jj", "new")
	if err != nil {
		return newOut, err
	}
	return describeOut + newOut, nil
}

func (Jujutsu) CurrentBranch(ctx context.Context, dir string) (string, error) {
	// jj doesn't always have a bookmark set; return the change ID instead.
	out, err := runVCSCmd(ctx, dir, "jj", "log", "-r", "@",
		"--no-graph", "-T", "change_id")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "main", nil // fallback
	}
	return branch, nil
}

func (Jujutsu) IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := runVCSCmd(ctx, dir, "jj", "st")
	if err != nil {
		return false, err
	}
	// jj prints "The working copy has no changes." when clean.
	return strings.Contains(out, "The working copy has no changes"), nil
}

// Ensure Jujutsu satisfies VCS at compile time.
var _ VCS = Jujutsu{}
