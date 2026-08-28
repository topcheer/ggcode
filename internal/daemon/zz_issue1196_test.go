package daemon

// Issue #1196 characteristic tests: daemonIdentityMatches must use the
// caller-provided workingDir (not $PWD) and must NOT treat a >24h-old PID
// file as evidence of staleness. The PID file is written exactly once at
// daemon start, so its mtime equals the start time; a 24h threshold evicted
// healthy long-running daemons, deleted their PID files, and allowed double
// daemons.

import (
	"os"
	"testing"
	"time"
)

// Core scenario: cmdline unavailable (empty) + process alive + correct
// workingDir => must NOT be judged stale, even when the PID file is older
// than 24h.
func TestIssue1196_LiveDaemonWithOldPIDFileNotStale(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Deliberately set PWD to an UNRELATED directory: the old implementation
	// derived the PID file path from $PWD and would stat the wrong file.
	t.Setenv("PWD", t.TempDir())

	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// Spawn a live child.
	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue1196]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	// Backdate the PID file to 25h ago: WritePIDFile writes once at start,
	// so an old mtime is normal for a long-running daemon.
	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(pidPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" } // cmdline unavailable
	t.Cleanup(func() { testProcessCmdline = orig })

	if !daemonIdentityMatches(child.Pid, workDir) {
		t.Fatal("REGRESSION: live daemon with >24h-old PID file judged stale despite correct workingDir (#1196)")
	}
}

// Dead process must still be rejected (empty cmdline fallback keeps the
// signal-0 aliveness check).
func TestIssue1196_DeadProcessStillRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	if daemonIdentityMatches(999999, t.TempDir()) {
		t.Fatal("REGRESSION: dead PID accepted as daemon (#1196)")
	}
}

// End-to-end through CheckExistingDaemon: with $PWD pointing elsewhere, a
// live daemon's PID file keyed to workingDir must be honored (not deleted).
func TestIssue1196_CheckExistingDaemonHonorsRealWorkingDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PWD", t.TempDir()) // unrelated to workDir

	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue1196-e2e]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-30 * time.Hour)
	if err := os.Chtimes(pidPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	pid, err := CheckExistingDaemon(workDir)
	if err != nil {
		t.Fatalf("CheckExistingDaemon: %v", err)
	}
	if pid != child.Pid {
		t.Fatalf("expected existing daemon pid %d, got %d (PID file wrongly evicted)", child.Pid, pid)
	}
	if _, statErr := os.Stat(pidPath); os.IsNotExist(statErr) {
		t.Fatal("REGRESSION: live daemon's PID file was deleted despite correct workingDir (#1196)")
	}
}
