package tool

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/diff"
)

// maxEditDiffLines limits the unified diff shown in edit/write tool results
// to prevent context bloat. Beyond this, only a summary is shown.
const maxEditDiffLines = 25

// compactDiff generates a concise unified diff between old and new file content
// for inclusion in edit/write tool results.
//
// Research basis: Claude Code, Cursor, and Cline all return the actual diff
// after editing a file. Without this feedback, the agent must re-read the file
// to verify the change — wasting tokens and context. A compact diff lets the
// agent self-verify in O(changed lines) instead of O(file size).
//
// Shows only changed lines with 1 line of surrounding context. The output is
// capped at maxEditDiffLines to keep it proportional to the change size, not
// the file size. Returns empty string if there are no changes.
func compactDiff(oldContent, newContent string) string {
	d := diff.UnifiedDiff(oldContent, newContent, 1)
	if d == "" {
		return ""
	}

	lines := strings.Split(strings.TrimRight(d, "\n"), "\n")

	// Count actual changed lines (+ and -) to decide whether to include the diff.
	changed := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "+ ") || strings.HasPrefix(l, "- ") {
			changed++
		}
	}
	if changed == 0 {
		return ""
	}

	// If too large, truncate and report remaining.
	if len(lines) > maxEditDiffLines {
		shown := lines[:maxEditDiffLines]
		// Count how many +/- lines we showed vs total
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
