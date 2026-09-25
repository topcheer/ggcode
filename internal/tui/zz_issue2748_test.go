package tui

import (
	"runtime"
	"testing"
	"time"
)

// #2748: a failed or timed-out statusline invocation must keep the last good
// cached output instead of blanking the bar.
func TestIssue2748FailedRefreshKeepsLastGoodOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based statusline script test is unix-only")
	}
	m := Model{}
	m.config = statuslineTestConfig()
	m.statusline = &statuslineState{seq: 1, text: "last-good", running: true}

	// A failing refresh (non-zero exit) arrives with the current seq.
	updated, _ := m.handleStatuslineMsg(statuslineMsg{seq: 2, text: "", ok: false})
	if got := updated.statusline.text; got != "last-good" {
		t.Fatalf("failed refresh clobbered cache: got %q, want last-good", got)
	}
	if updated.statusline.running {
		t.Fatal("running flag must be cleared even on failed refresh")
	}

	// A timed-out refresh is the same ok=false path.
	updated, _ = m.handleStatuslineMsg(statuslineMsg{seq: 3, text: "", ok: false})
	if got := updated.statusline.text; got != "last-good" {
		t.Fatalf("timeout refresh clobbered cache: got %q, want last-good", got)
	}

	// A successful refresh still replaces the cache.
	updated, _ = m.handleStatuslineMsg(statuslineMsg{seq: 4, text: "fresh", ok: true})
	if got := updated.statusline.text; got != "fresh" {
		t.Fatalf("successful refresh did not update cache: got %q, want fresh", got)
	}
}

// End-to-end variant: runStatuslineCommand's failure return must carry ok=false
// so the goroutine in refreshStatusline can distinguish failure from a
// legitimately empty successful line.
func TestIssue2748RunCommandFailureSignalsNotOK(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-based statusline script test is unix-only")
	}
	if _, ok := runStatuslineCommand(`exit 1`, statuslinePayload{}, time.Second); ok {
		t.Fatal("non-zero exit must return ok=false")
	}
	if _, ok := runStatuslineCommand(`sleep 5`, statuslinePayload{}, 80*time.Millisecond); ok {
		t.Fatal("timeout must return ok=false")
	}
	if text, ok := runStatuslineCommand(`printf 'line'`, statuslinePayload{}, time.Second); !ok || text != "line" {
		t.Fatalf("success = (%q, %v), want (line, true)", text, ok)
	}
}
