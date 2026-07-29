package vcs

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Git implements VCS for git repositories.
type Git struct{}

func (Git) Name() string        { return "git" }
func (Git) DisplayName() string { return "Git" }

func (Git) Status(ctx context.Context, dir string) (string, error) {
	return runVCSCmd(ctx, dir, "git", "status", "--short")
}

func (Git) Diff(ctx context.Context, dir string, cached bool, file string) (string, error) {
	args := []string{"diff"}
	if cached {
		args = append(args, "--cached")
	}
	if file != "" {
		args = append(args, "--", file)
	}
	return runVCSCmd(ctx, dir, "git", args...)
}

func (Git) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}
	return runVCSCmd(ctx, dir, "git", "log", "--oneline", "-"+strconv.Itoa(count))
}

func (Git) Add(ctx context.Context, dir string, files []string) (string, error) {
	args := []string{"add", "--"}
	args = append(args, files...)
	return runVCSCmd(ctx, dir, "git", args...)
}

func (Git) Commit(ctx context.Context, dir string, message string) (string, error) {
	return runVCSCmd(ctx, dir, "git", "commit", "-m", message)
}

func (Git) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := runVCSCmd(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (Git) IsClean(ctx context.Context, dir string) (bool, error) {
	out, err := runVCSCmd(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// AheadBehind returns the number of commits the local branch is ahead of and
// behind its upstream tracking branch. ok is false if there is no upstream
// configured or the command fails.
func (Git) AheadBehind(ctx context.Context, dir string) (ahead, behind int, ok bool) {
	out, err := runVCSCmd(ctx, dir, "git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, false
	}
	b, err1 := strconv.Atoi(fields[0])
	a, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return a, b, true
}

// runVCSCmd executes a VCS command in the given directory.
func runVCSCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	stdout, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(stdout), nil
}
