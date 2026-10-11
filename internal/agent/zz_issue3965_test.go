package agent

import (
	"os"
	"strings"
	"testing"
)

// #3965 guard: the proactive rate-limit check must actually emit a
// StreamEventSystem warning before a 429 lands - the comment promised it
// but only debug.Log ever ran, leaving TUI/desktop/IM blind until the
// interruption. The wiring lives inline in the streamDone branch of the
// run loop; a behavioral harness for the full provider stream path is
// disproportionate (same disclosure as #3945/#3959 source-scan guards).
func TestIssue3965RateLimitWarningEmitsEvent(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	anchor := strings.Index(text, "Proactive rate-limit check")
	if anchor < 0 {
		t.Fatal("rate-limit check block not found")
	}
	// Bound the scan window to this branch (up to the next hook comment).
	end := strings.Index(text[anchor:], "on_stream_stop hook")
	if end < 0 {
		t.Fatal("branch end anchor not found")
	}
	branch := text[anchor : anchor+end]
	if !strings.Contains(branch, "StreamEventSystem") {
		t.Error("rate-limit critical branch emits no StreamEventSystem - user-facing warning silently downgraded to debug.Log (#3965 regression)")
	}
	// Edge-trigger: the branch must consult the latch (no per-turn spam).
	if !strings.Contains(branch, "!a.rlCriticalWarned") {
		t.Error("rate-limit warning lacks edge-trigger latch - persistent critical quota would emit a system event every turn")
	}
	// Re-arm: a healthy concrete reading must clear the latch so a later
	// critical episode warns again.
	if !strings.Contains(branch, "a.rlCriticalWarned = false") {
		t.Error("rate-limit warning latch is never re-armed on quota recovery - only the first critical episode per run would be visible")
	}
}
