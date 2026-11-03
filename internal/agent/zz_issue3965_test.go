package agent

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3965RateLimitWarningEmitsEvent(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	anchor := strings.Index(text, "if rl, ok := a.provider.(provider.RateLimitProvider); ok {")
	if anchor < 0 {
		t.Fatal("rate-limit check block not found")
	}

	endRel := strings.Index(text[anchor:], "a.rlCriticalWarned = false")
	if endRel < 0 {
		t.Fatal("branch end anchor not found")
	}
	branch := text[anchor : anchor+endRel+len("a.rlCriticalWarned = false")]
	if !strings.Contains(branch, "StreamEventSystem") {
		t.Error("rate-limit critical branch emits no StreamEventSystem - user-facing warning silently downgraded to debug.Log (#3965 regression)")
	}

	if !strings.Contains(branch, "!a.rlCriticalWarned") {
		t.Error("rate-limit warning lacks edge-trigger latch - persistent critical quota would emit a system event every turn")
	}

	if !strings.Contains(branch, "a.rlCriticalWarned = false") {
		t.Error("rate-limit warning latch is never re-armed on quota recovery - only the first critical episode per run would be visible")
	}
}
