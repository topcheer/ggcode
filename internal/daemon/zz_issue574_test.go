package daemon

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIssue574D_WritePIDFileFlockPreventsConcurrentFork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := WritePIDFile(pidPath, 1000, "sess1", workDir); err != nil {
		t.Fatalf("first WritePIDFile should succeed: %v", err)
	}

	err = WritePIDFile(pidPath, 2000, "sess2", workDir)
	if err == nil {
		t.Fatal("REGRESSION: second WritePIDFile should fail with flock held (#574-D)")
	}
	if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "concurrent") {
		t.Errorf("expected lock-related error, got: %v", err)
	}
}

func TestIssue574D_WritePIDFileFlockAllowsReuseAfterUnlock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := WritePIDFile(pidPath, 1000, "sess1", workDir); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(pidPath); err != nil {
		t.Fatal(err)
	}

	if err := WritePIDFile(pidPath, 2000, "sess2", workDir); err != nil {
		t.Fatalf("second WritePIDFile should succeed after first is removed: %v", err)
	}
}

func TestIssue574C_DaemonIdentityMatchesDeadProcessReturnsFalse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PWD", t.TempDir())

	deadPid := 999999

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	if daemonIdentityMatches(deadPid, t.TempDir()) {
		t.Fatal("REGRESSION: daemonIdentityMatches returned true for dead PID with empty cmdline (#574-C)")
	}
}

func TestIssue574C_DaemonIdentityMatchesLiveProcessRecentFileReturnsTrue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	t.Setenv("PWD", workDir)

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue574c]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	if !daemonIdentityMatches(child.Pid, workDir) {
		t.Fatal("daemonIdentityMatches should return true for live process with recent PID file")
	}
}

func TestIssue574C_DaemonIdentityMatchesLiveProcessOldFileReturnsTrue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	t.Setenv("PWD", workDir)

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue574c-stale]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(pidPath, staleTime, staleTime); err != nil {
		t.Fatalf("backdating PID file: %v", err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	if !daemonIdentityMatches(child.Pid, workDir) {
		t.Fatal("REGRESSION: live daemon with >24h-old PID file wrongly judged stale (#1196)")
	}
}

func TestIssue574C_CheckExistingDaemonCleansUpStaleWithEmptyCmdline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf(`{"pid": 999999, "session_id": "sess", "working_dir": "%s", "started_at": "2024-01-01T00:00:00Z"}`, workDir)), 0o644); err != nil {
		t.Fatal(err)
	}

	staleTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(pidPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

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

func TestIssue574C_CleanupDaemonRemovesDeadProcessPIDFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	deadPid := 999999
	if err := WritePIDFile(pidPath, deadPid, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	CleanupDaemon(workDir)

	if daemonIdentityMatches(deadPid, t.TempDir()) {
		t.Fatal("REGRESSION: daemonIdentityMatches should return false for dead process (#574-C)")
	}
}
