package vcs

import (
	"context"
	"strconv"
	"strings"
)

type Jujutsu struct{}

func (Jujutsu) Name() string        { return "jj" }
func (Jujutsu) DisplayName() string { return "Jujutsu" }

func (Jujutsu) Status(ctx context.Context, dir string) (string, error) {

	out, err := runVCSCmd(ctx, dir, "jj", "diff", "-r", "@", "--summary")
	if err == nil {
		return out, nil
	}
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

	return runVCSCmd(ctx, dir, "jj", "log",
		"-n", strconv.Itoa(count),
		"--no-graph",
		"-T", `commit_id.short() ++ " " ++ description.first_line() ++ "\n"`)
}

func (Jujutsu) Add(ctx context.Context, dir string, files []string) (string, error) {

	args := []string{"file", "track", "--"}
	args = append(args, files...)
	return runVCSCmd(ctx, dir, "jj", args...)
}

func (Jujutsu) Commit(ctx context.Context, dir string, message string) (string, error) {

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

	out, err := runVCSCmd(ctx, dir, "jj", "log", "-r", "@",
		"--no-graph", "-T", "change_id")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "main", nil
	}
	return branch, nil
}

func (Jujutsu) IsClean(ctx context.Context, dir string) (bool, error) {

	out, err := runVCSCmd(ctx, dir, "jj", "diff", "-r", "@", "--summary")
	if err == nil {
		return strings.TrimSpace(out) == "", nil
	}

	out, err = runVCSCmd(ctx, dir, "jj", "st")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "The working copy has no changes") ||
		strings.Contains(out, "The working copy is clean"), nil
}

func (Jujutsu) Checkout(ctx context.Context, dir, branch string, create bool, startPoint string) (string, error) {
	if create {
		args := []string{"bookmark", "create", branch}
		if startPoint != "" {
			args = append(args, "-r", startPoint)
		}
		out, err := runVCSCmd(ctx, dir, "jj", args...)
		if err != nil {
			return out, err
		}

		switchOut, err := runVCSCmd(ctx, dir, "jj", "new", branch)
		return out + "\n" + switchOut, err
	}
	return runVCSCmd(ctx, dir, "jj", "new", branch)
}

var _ VCS = Jujutsu{}
