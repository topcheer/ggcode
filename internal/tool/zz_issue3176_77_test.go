package tool

// Regression probes for #3176 + #3177: tool-output truncation consistency.
//
// #3176: list_agents streamed the full sub-agent Result (up to the
// manager's 100KB cap) on one line while every sibling field truncates.
// #3177: list_mcp_capabilities joined tool/prompt/resource name lists
// with no cap while the same file caps CONTENT at 50KB.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
)

func TestIssue3176_ResultTruncated(t *testing.T) {
	long := strings.Repeat("R", 5000)
	snap := subagent.Snapshot{
		Status: subagent.StatusCompleted,
		Result: long,
		Task:   "gen report",
	}
	out := formatSubAgentSnapshot(snap)
	if n := strings.Count(out, "R"); n > 250 {
		t.Errorf("Result must be truncated to ~200 chars, got %d Rs on the line", n)
	}
	if !strings.Contains(out, "Result:") {
		t.Error("Result line must still be present")
	}
	// The truncated form keeps the ellipsis marker of util.Truncate.
	if !strings.Contains(out, "...") {
		t.Errorf("truncated Result should carry the ellipsis marker, got tail: %s", tail3176(out, 120))
	}
}

func TestIssue3177_JoinOrNoneCapped(t *testing.T) {
	// Small list: unchanged, no cap marker.
	small := joinOrNone([]string{"alpha", "beta"})
	if small != "alpha, beta" {
		t.Errorf("small list must join verbatim, got %q", small)
	}
	// Huge list: capped at ~4KB with a "+N more (of M total)" summary.
	var names []string
	for i := 0; i < 500; i++ {
		names = append(names, strings.Repeat("n", 40)+string(rune('a'+i%26)))
	}
	big := joinOrNone(names)
	if len(big) > maxListOutputBytes+80 {
		t.Errorf("joined list len=%d exceeds cap+summary budget %d", len(big), maxListOutputBytes+80)
	}
	if !strings.Contains(big, "+ ") || !strings.Contains(big, "more (of 500 total)") {
		t.Errorf("capped list must summarize omissions, got tail: %s", tail3176(big, 100))
	}
	// Empty list keeps the (none) sentinel.
	if got := joinOrNone(nil); got != "(none)" {
		t.Errorf("empty list sentinel changed: %q", got)
	}
}

func TestIssue3177_JoinCappedKeepsAtLeastOne(t *testing.T) {
	// A single name longer than the budget is still shown (never an empty
	// list); overflow spills to the summary on the next value.
	got := joinCapped([]string{strings.Repeat("x", 100), "second"}, 10)
	if !strings.HasPrefix(got, "xxx") {
		t.Errorf("first value must be shown even when over budget, got %q", tail3176(got, 40))
	}
}

func tail3176(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
