package tui

// #2748 probes: the keep-last-good contract from #2680 (and the promise in
// this file's own header) said a failed or timed-out statusline refresh
// keeps the last good output. Pre-fix, runStatuslineCommand returned ""
// on failure and handleStatuslineMsg unconditionally overwrote the cached
// text with it - one flaky script run blanked the status line until the
// next successful refresh.

import (
	"testing"
	"time"
)

// Contract: a timed-out refresh after a good one must NOT clear the cache.
func TestIssue2748TimeoutKeepsLastGoodOutput(t *testing.T) {
	// 1. Good refresh lands.
	m := Model{}
	m.config = statuslineTestConfig()
	m.statusline = &statuslineState{seq: 1}
	updated, _ := m.handleStatuslineMsg(statuslineMsg{seq: 1, text: "model: glm-5.3 | ctx 42%", ok: true})
	if got := updated.statusline.text; got != "model: glm-5.3 | ctx 42%" {
		t.Fatalf("good refresh not cached: %q", got)
	}
	if updated.statusline.running {
		t.Fatalf("running must clear after a completed refresh")
	}

	// 2. The NEXT refresh times out (ok=false). The seq is consumed but
	// the cached text must survive - pre-fix this blanked the status line.
	updated, _ = updated.handleStatuslineMsg(statuslineMsg{seq: 2, ok: false})
	if got := updated.statusline.text; got != "model: glm-5.3 | ctx 42%" {
		t.Fatalf("timed-out refresh cleared last good output: %q", got)
	}
	if updated.statusline.running {
		t.Fatalf("running must clear even on failure (else all future refreshes wedge)")
	}
}

// End-to-end through runStatuslineCommand: failing command yields ok=false.
func TestIssue2748FailingCommandReportsNotOK(t *testing.T) {
	cmd := statuslineTestCommand(t, `exit 7`)
	got, ok := runStatuslineCommand(cmd, statuslinePayload{}, time.Second)
	if ok || got != "" {
		t.Fatalf("failing run = (%q, %v), want empty/false", got, ok)
	}
	// And the failure path through handleStatuslineMsg keeps the cache.
	m := Model{}
	m.config = statuslineTestConfig()
	m.statusline = &statuslineState{seq: 5, text: "good"}
	updated, _ := m.handleStatuslineMsg(statuslineMsg{seq: 6, ok: ok})
	if got := updated.statusline.text; got != "good" {
		t.Fatalf("failed refresh overwrote cache: %q", got)
	}
}
