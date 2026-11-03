package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckExistingDaemon_TransientReadErrorKeepsPIDFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatalf("PIDFilePath: %v", err)
	}
	if err := WritePIDFile(pidPath, 4242, "sess", workDir); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}

	makePIDFileUnreadable(t, pidPath)

	pid, err := CheckExistingDaemon(workDir)
	_ = pid
	if err == nil {
		t.Fatal("expected an error for unreadable PID file, got nil")
	}
	if _, statErr := os.Stat(pidPath); os.IsNotExist(statErr) {
		t.Fatal("REGRESSION: PID file was deleted on transient read error (double-daemon risk)")
	}
}

func TestCheckExistingDaemon_CorruptPIDFileRemoved(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatalf("PIDFilePath: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt pid: %v", err)
	}

	pid, err := CheckExistingDaemon(workDir)
	if err != nil {
		t.Fatalf("corrupt PID file should be handled as no-daemon, got err: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected pid 0, got %d", pid)
	}
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Error("corrupt PID file should have been removed (#431)")
	}
}

func TestCheckExistingDaemon_MissingPIDFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	pid, err := CheckExistingDaemon(workDir)
	if err != nil {
		t.Fatalf("missing PID file should be (0, nil), got err: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected pid 0, got %d", pid)
	}
}

var _ = filepath.Join
