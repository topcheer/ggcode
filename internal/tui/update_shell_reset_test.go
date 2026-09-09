package tui

import (
	"testing"
)

// #1781: the shell-done handler cleared shellOwnedLoading at the top and
// then tested it twenty lines later for the #915 runCanceled/runFailed
// reset - dead code. A shell-owned cancel leaked runCanceled into the
// next normal agent completion (persist + pendingSubmission skipped).
func TestShellDoneClearsCancelStateWhenShellOwnedRun(t *testing.T) {
	m := newTestModel()
	// Shell owns the loading state; user pressed Esc during it.
	m.shellOwnedLoading = true
	m.setLoading(true)
	m.runCanceled = true
	m.runFailed = true
	m.shellRunning = true

	m2, _ := m.Update(shellCommandDoneMsg{RunID: 0})
	mm, ok := m2.(Model)
	if !ok {
		t.Fatalf("shell done returned %T, want Model", m2)
	}
	if mm.runCanceled {
		t.Fatal("shell-owned run must reset runCanceled on shell completion (was dead code)")
	}
	if mm.runFailed {
		t.Fatal("shell-owned run must reset runFailed on shell completion")
	}
	if mm.shellOwnedLoading {
		t.Fatal("shell completion must clear shellOwnedLoading")
	}
	if mm.loading {
		t.Fatal("shell-owned loading must be cleared on shell completion")
	}
}
