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
		args = append(args, "--", file)
	}
	return runVCSCmd(ctx, dir, "svn", args...)
}

func (Subversion) Log(ctx context.Context, dir string, count int) (string, error) {
	if count <= 0 {
		count = 10
	}
	// #1022: -r X:Y sets the traversal direction and -l N stops after N
	// entries — 1:HEAD walked from the OLDEST revision, silently returning
	// r1..rN instead of the newest N (interface contract: recent history).
	// HEAD:1 walks newest-first.
	// #1493: raw svn log output is multi-line (---- separators, r42|author
	// headers, message bodies), violating the one-entry-per-line contract
	// every other VCS honors; recent_commits then hard-sliced the noise into
	// the system prompt. Normalize each revision to a single line here.
	out, err := runVCSCmd(ctx, dir, "svn", "log", "-r", "HEAD:1", "-l", strconv.Itoa(count))
	if err != nil {
		return out, err
	}
	return normalizeSvnLog(out), nil
}

// normalizeSvnLog collapses svn's multi-line log format into one line per
// revision: "r42 | author | date | message-first-line".
func normalizeSvnLog(log string) string {
	var lines []string
	for _, block := range strings.Split(log, "------------------------------------------------------------------------") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		// First line of each block: r42 | author | date | line-count | path?
		parts := strings.SplitN(block, "\n", 2)
		header := strings.TrimSpace(parts[0])
		msg := ""
		if len(parts) > 1 {
			// Skip the header's line-count continuation lines; the message
			// starts after a blank line. Keep its first line only.
			bodyLines := strings.Split(parts[1], "\n")
			for i, l := range bodyLines {
				if strings.TrimSpace(l) != "" {
					// message body: all non-empty lines after the first blank
					// separator; simplest robust pick = first non-empty line that
					// is not part of the header (headers contain ' | ').
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
	// #1407-B: 'svn status' lists svn:externals definitions with a
	// leading 'X' (not a local modification - git has no equivalent) and
	// in-progress externals with '>'. Without filtering, a working copy
	// with externals reports dirty forever despite zero local changes.
	// '?' (unversioned) stays counted - same semantics as git's '??'.
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.TrimSpace(t) == "" {
			continue
		}
		if c := t[0]; c == 'X' || c == '>' {
			continue
		}
		return false, nil
	}
	return true, nil
}

// Checkout is not supported for Subversion (no branch concept).
func (Subversion) Checkout(ctx context.Context, dir, branch string, create bool, startPoint string) (string, error) {
	return "", ErrCheckoutNotSupported
}

// Ensure Subversion satisfies VCS at compile time.
var _ VCS = Subversion{}
