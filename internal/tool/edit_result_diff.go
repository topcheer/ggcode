package tool

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/diff"
)

const maxEditDiffLines = 25

func compactDiff(oldContent, newContent string) string {
	d := diff.UnifiedDiff(oldContent, newContent, 1)
	if d == "" {
		return ""
	}

	lines := strings.Split(strings.TrimRight(d, "\n"), "\n")

	changed := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "+ ") || strings.HasPrefix(l, "- ") {
			changed++
		}
	}
	if changed == 0 {
		return ""
	}

	if len(lines) > maxEditDiffLines {
		shown := lines[:maxEditDiffLines]

		shownChanged := 0
		for _, l := range shown {
			if strings.HasPrefix(l, "+ ") || strings.HasPrefix(l, "- ") {
				shownChanged++
			}
		}
		remaining := changed - shownChanged
		if remaining > 0 {
			shown = append(shown, fmt.Sprintf("(%d more changed lines)", remaining))
		}
		lines = shown
	}

	return "\n\n[Changes]\n" + strings.Join(lines, "\n")
}
