package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/daemon"
)

// TestIssue2633DetachForkFailureStaysForeground verifies that
// detachToBackground reports failure (returns false) when the daemon slot
// check errors, so the 'd' keypress handler in runDaemon keeps the session
// alive in the foreground instead of breaking the loop and exiting (#2633).
func TestIssue2633DetachForkFailureStaysForeground(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workingDir := t.TempDir()
	pidPath, err := daemon.PIDFilePath(workingDir)
	if err != nil {
		t.Fatalf("PIDFilePath: %v", err)
	}

	// Make the PID file unreadable in a way that is neither NotExist nor a
	// JSON syntax/type error: a directory at the PID file path. Per #520,
	// CheckExistingDaemon must propagate this error (not delete the slot),
	// which makes EnsureDaemonSlot fail inside detachToBackground.
	if err := os.MkdirAll(pidPath, 0o755); err != nil {
		t.Fatalf("mkdir pid path: %v", err)
	}

	got := detachToBackground(daemon.LangEn, "", workingDir, "sess-2633")
	if got != false {
		t.Fatalf("detachToBackground returned %v on slot-check failure; want false (stay foreground)", got)
	}

	// Sanity: the unreadable slot must not have been silently removed.
	if _, err := os.Stat(filepath.Join(filepath.Dir(pidPath))); err != nil {
		t.Fatalf("daemon dir vanished: %v", err)
	}
}
