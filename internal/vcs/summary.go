package vcs

import (
	"context"
	"fmt"
	"strings"
)

// Summary returns a rich one-line status summary of the repository in
// workingDir, including the VCS name, current branch, dirty file count,
// and ahead/behind tracking (git only). It is safe to call in any directory
// and never returns an empty string.
func Summary(ctx context.Context, workingDir string) string {
	v := Detect(workingDir)
	if v == nil {
		return "not a version-controlled repository"
	}

	var parts []string
	parts = append(parts, "in a "+v.DisplayName()+" repository")

	// Current branch / head name
	if branch, err := v.CurrentBranch(ctx, workingDir); err == nil && strings.TrimSpace(branch) != "" {
		parts = append(parts, "on "+strings.TrimSpace(branch))
	}

	// Dirty state with file count
	if clean, err := v.IsClean(ctx, workingDir); err == nil {
		if clean {
			parts = append(parts, "clean working tree")
		} else {
			if status, serr := v.Status(ctx, workingDir); serr == nil {
				count := countStatusLines(status)
				if count > 0 {
					parts = append(parts, fmt.Sprintf("%d uncommitted file(s)", count))
				} else {
					parts = append(parts, "dirty working tree")
				}
			}
		}
	}

	// Ahead/behind upstream (git only)
	if g, ok := v.(Git); ok {
		if ahead, behind, ok := g.AheadBehind(ctx, workingDir); ok && (ahead > 0 || behind > 0) {
			var ab []string
			if ahead > 0 {
				ab = append(ab, fmt.Sprintf("%d ahead", ahead))
			}
			if behind > 0 {
				ab = append(ab, fmt.Sprintf("%d behind", behind))
			}
			parts = append(parts, strings.Join(ab, ", "))
		}
	}

	return strings.Join(parts, ", ")
}

// countStatusLines counts non-empty lines in a short-format VCS status output.
func countStatusLines(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
