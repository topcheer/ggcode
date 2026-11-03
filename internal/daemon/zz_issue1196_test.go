package daemon

import (
	"os"
	"testing"
	"time"
)

func TestIssue1196_LiveDaemonWithOldPIDFileNotStale(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Setenv("PWD", t.TempDir())

	workDir := t.TempDir()
	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	child, err := os.StartProcess("/bin/sleep", []string{"ggcode[issue1196]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn test child (unsupported platform?): %v", err)
	}
	t.Cleanup(func() { _ = child.Kill() })

	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	stale := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(pidPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	if !daemonIdentityMatches(child.Pid, workDir) {
		t.Fatal("REGRESSION: live daemon with >24h-old PID file judged stale despite correct workingDir (#1196)")
	}
}

func TestIssue1196_DeadProcessStillRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := testProcessCmdline
	testProcessCmdline = func(int) string { return "" }
	t.Cleanup(func() { testProcessCmdline = orig })

	if daemonIdentityMatches(999999, t.TempDir()) {
		t.Fatal("REGRESSION: dead PID accepted as daemon (#1196)")
	}
}

func TestIssue1196_CheckExistingDaemonHonorsRealWorkingDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PWD", t.TempDir())

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
