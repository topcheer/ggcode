package tui

import (
	"testing"
)

func TestShellDoneClearsCancelStateWhenShellOwnedRun(t *testing.T) {
	m := newTestModel()

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
