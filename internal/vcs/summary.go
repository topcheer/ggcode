package vcs

import (
	"context"
	"fmt"
	"strings"
)

func Summary(ctx context.Context, workingDir string) string {
	v := Detect(workingDir)
	if v == nil {
		return "not a version-controlled repository"
	}

	var parts []string
	parts = append(parts, "in a "+v.DisplayName()+" repository")

	if branch, err := v.CurrentBranch(ctx, workingDir); err == nil && strings.TrimSpace(branch) != "" {
		parts = append(parts, "on "+strings.TrimSpace(branch))
	}

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

func countStatusLines(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
