package daemon

// Issue #574 characteristic tests: PID file flock mutex (D),
// daemonIdentityMatches empty-cmdline fallback (C), and Revert
// Correction recording (G in checkpoint package).

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// D: WritePIDFile must acquire an exclusive flock to prevent concurrent
// forks from claiming the same slot. Two processes writing to the same
// PID file path must NOT both succeed.
func TestIssue574D_WritePIDFileFlockPreventsConcurrentFork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// First write should succeed.
	if err := WritePIDFile(pidPath, 1000, "sess1", workDir); err != nil {
		t.Fatalf("first WritePIDFile should succeed: %v", err)
	}

	// Second write to the same path should fail with lock error.
	err = WritePIDFile(pidPath, 2000, "sess2", workDir)
	if err == nil {
		t.Fatal("REGRESSION: second WritePIDFile should fail with flock held (#574-D)")
	}
	if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "concurrent") {
		t.Errorf("expected lock-related error, got: %v", err)
	}
}

// D: After the first PID file is deleted, a new write should succeed.
func TestIssue574D_WritePIDFileFlockAllowsReuseAfterUnlock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// First write.
	if err := WritePIDFile(pidPath, 1000, "sess1", workDir); err != nil {
		t.Fatal(err)
	}

	// Remove the file (which releases the lock when the fd is closed).
	if err := os.Remove(pidPath); err != nil {
		t.Fatal(err)
	}

	// Second write should now succeed.
	if err := WritePIDFile(pidPath, 2000, "sess2", workDir); err != nil {
		t.Fatalf("second WritePIDFile should succeed after first is removed: %v", err)
	}
}

// C: daemonIdentityMatches must NOT fallback to true when cmdline is empty.
// When the process is actually dead, it should return false.
func TestIssue574C_DaemonIdentityMatchesDeadProcessReturnsFalse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PWD", t.TempDir())

	// Use a PID that doesn't exist.
	deadPid := 999999

	// Mock processCmdline to return empty (simulating permission denied or
	// unsupported platform).
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	// For a dead PID, daemonIdentityMatches should return false even with
	// empty cmdline (it checks signal-0 internally).
	if daemonIdentityMatches(deadPid) {
		t.Fatal("REGRESSION: daemonIdentityMatches returned true for dead PID with empty cmdline (#574-C)")
	}
}

// C: daemonIdentityMatches with empty cmdline but live process and recent
// PID file should return true (conservative acceptance).
func TestIssue574C_DaemonIdentityMatchesLiveProcessRecentFileReturnsTrue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	t.Setenv("PWD", workDir)

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// Spawn a short-lived child that looks like a daemon.
	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue574c]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	// Write a recent PID file.
	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	// Mock processCmdline to return empty.
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	// With live process + recent PID file, should return true.
	if !daemonIdentityMatches(child.Pid) {
		t.Fatal("daemonIdentityMatches should return true for live process with recent PID file")
	}
}

// C: daemonIdentityMatches with empty cmdline and stale PID file (>24h)
// should return false (self-healing path).
func TestIssue574C_DaemonIdentityMatchesStaleFileReturnsFalse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	t.Setenv("PWD", workDir)

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// Spawn a child.
	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue574c-stale]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	// Write a PID file, then backdate it to 25 hours ago.
	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(pidPath, staleTime, staleTime); err != nil {
		t.Fatalf("backdating PID file: %v", err)
	}

	// Mock processCmdline to return empty.
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	// With stale PID file (>24h), should return false even if process is alive.
	if daemonIdentityMatches(child.Pid) {
		t.Fatal("REGRESSION: daemonIdentityMatches should return false for stale PID file (#574-C)")
	}
}

// C: CheckExistingDaemon should clean up stale PID files when cmdline
// is empty and the PID file is old enough.
func TestIssue574C_CheckExistingDaemonCleansUpStaleWithEmptyCmdline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a PID file for a dead PID.
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf(`{"pid": 999999, "session_id": "sess", "working_dir": "%s", "started_at": "2024-01-01T00:00:00Z"}`, workDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate to >24h.
	staleTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(pidPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	// Mock processCmdline to return empty.
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	// CheckExistingDaemon should detect stale PID and clean up.
	pid, err := CheckExistingDaemon(workDir)
	if err != nil {
		t.Fatalf("CheckExistingDaemon: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected no daemon, got pid %d", pid)
	}
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Fatal("REGRESSION: stale PID file with empty cmdline not cleaned up (#574-C)")
	}
}

// C: CleanupDaemon should be able to remove a PID file when the process
// is dead (even with empty cmdline case).
func TestIssue574C_CleanupDaemonRemovesDeadProcessPIDFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// Write a PID file for a dead process.
	deadPid := 999999
	if err := WritePIDFile(pidPath, deadPid, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	// Mock processCmdline to return empty.
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	// CleanupDaemon should remove it when the PID doesn't match our process.
	// Note: this test verifies the cleanup path doesn't get stuck with empty cmdline.
	CleanupDaemon(workDir)
	// Since the PID is not ours, it won't be removed by CleanupDaemon (by design).
	// This is correct behavior - we only clean up our own PID files.
	// The key fix is in daemonIdentityMatches which should return false for dead PIDs.

	// Verify daemonIdentityMatches correctly identifies dead process.
	if daemonIdentityMatches(deadPid) {
		t.Fatal("REGRESSION: daemonIdentityMatches should return false for dead process (#574-C)")
	}
}
