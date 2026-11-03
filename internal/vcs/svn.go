package vcs

import (
	"context"
	"strconv"
	"strings"
)

type Subversion struct{}

func (Subversion) Name() string        { return "svn" }
func (Subversion) DisplayName() string { return "Subversion" }

func (Subversion) Status(ctx context.Context, dir string) (string, error) {
	return runVCSCmd(ctx, dir, "svn", "status")
}

func (Subversion) Diff(ctx context.Context, dir string, cached bool, file string) (string, error) {

	args := []string{"diff"}
	if file != "" {
		args = append(args, "--", file)
	}
	return runVCSCmd(ctx, dir, "svn", args...)
}

func (Subversion) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}

	out, err := runVCSCmd(ctx, dir, "svn", "log", "-r", "HEAD:1", "-l", strconv.Itoa(count))
	if err != nil {
		return out, err
	}
	return normalizeSvnLog(out), nil
}

func normalizeSvnLog(log string) string {
	var lines []string
	for _, block := range strings.Split(log, "------------------------------------------------------------------------") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		parts := strings.SplitN(block, "\n", 2)
		header := strings.TrimSpace(parts[0])
		msg := ""
		if len(parts) > 1 {

			bodyLines := strings.Split(parts[1], "\n")
			for i, l := range bodyLines {
				if strings.TrimSpace(l) != "" {

					if !strings.Contains(l, " | ") || i > 0 {
						msg = strings.TrimSpace(l)
						break
					}
				}
			}
		}
		if msg != "" {
			lines = append(lines, header+" | "+msg)
		} else {
			lines = append(lines, header)
		}
	}
	return strings.Join(lines, "\n")
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

	out, err := runVCSCmd(ctx, dir, "svn", "info", "--show-item", "url")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)

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

	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.TrimSpace(t) == "" {
			continue
		}
		c := t[0]
		if c == 'X' || c == '>' {
			continue
		}

		if c == 'P' {
			continue
		}
		return false, nil
	}
	return true, nil
}

func (Subversion) Checkout(ctx context.Context, dir, branch string, create bool, startPoint string) (string, error) {
	return "", ErrCheckoutNotSupported
}

var _ VCS = Subversion{}
