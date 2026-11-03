package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/daemon"
)

func TestIssue2633DetachForkFailureStaysForeground(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workingDir := t.TempDir()
	pidPath, err := daemon.PIDFilePath(workingDir)
	if err != nil {
		t.Fatalf("PIDFilePath: %v", err)
	}

	if err := os.MkdirAll(pidPath, 0o755); err != nil {
		t.Fatalf("mkdir pid path: %v", err)
	}

	got := detachToBackground(daemon.LangEn, "", workingDir, "sess-2633", nil)
	if got != false {
		t.Fatalf("detachToBackground returned %v on slot-check failure; want false (stay foreground)", got)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(pidPath))); err != nil {
		t.Fatalf("daemon dir vanished: %v", err)
	}
}
