package daemon

// Issue #552 characteristic tests: double-start guard wiring (A), /dev/null
// stdin for the daemonized child (B), PID-file ownership on cleanup (E),
// and stale PID file removal on fork failure (C).

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spawnDaemonLikeChild starts a short-lived child whose argv[0] contains the
// "ggcode[" marker so daemonIdentityMatches accepts it as a daemon. This
// exercises the real identity-verification chain without a true ggcode fork.
func spawnDaemonLikeChild(t *testing.T) *os.Process {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	_ = bin
	proc, err := os.StartProcess("/bin/sleep", []string{"ggcode[zz-issue552]", "30"}, &os.ProcAttr{})
	if err != nil {
		t.Fatalf("spawn helper: %v (unsupported platform?)", err)
	}
	return proc
}

// A: EnsureDaemonSlot must reject when a live daemon owns the working dir,
// and allow once the daemon is gone.
func TestIssue552A_EnsureDaemonSlotRejectsLiveDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	// No PID file yet → slot free.
	if err := EnsureDaemonSlot(workDir); err != nil {
		t.Fatalf("empty slot should be allowed, got: %v", err)
	}

	child := spawnDaemonLikeChild(t)
	t.Cleanup(func() { _ = child.Kill() })

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	err = EnsureDaemonSlot(workDir)
	if err == nil {
		t.Fatal("REGRESSION: live daemon did not block a second start (#552-A)")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected error: %v", err)
	}

	// Kill the daemon → stale PID file is cleaned → slot free again.
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = child.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := EnsureDaemonSlot(workDir); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot never freed after daemon death")
}

// A: EnsureDaemonSlot must propagate unreadable-PID-file errors (#520
// semantics preserved through the new guard).
func TestIssue552A_EnsureDaemonSlotPropagatesReadError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePIDFile(pidPath, 4242, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pidPath, 0o000); err != nil {
		t.Skipf("cannot chmod (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(pidPath, 0o644) })

	if err := EnsureDaemonSlot(workDir); err == nil {
		t.Fatal("unreadable PID file must produce an error, not a silent allow")
	}
}

// B: the daemonized child's stdin must be /dev/null, never the parent tty.
func TestIssue552B_ForkStdinIsDevNull(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	var gotStdinName string
	orig := osStartProcess
	osStartProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		if len(attr.Files) > 0 {
			// Inspect before ForkIntoBackground closes the file.
			gotStdinName = attr.Files[0].Name()
		}
		return nil, syscall.ENOENT
	}
	t.Cleanup(func() { osStartProcess = orig })

	_, _ = ForkIntoBackground("cfg", workDir, "sess")

	if gotStdinName == "" {
		t.Fatal("start hook not invoked")
	}
	if gotStdinName != os.DevNull {
		t.Fatalf("REGRESSION: daemon stdin is %q, want %q (#552-B: inheriting the tty breaks echo and SSH EOF kills the daemon)", gotStdinName, os.DevNull)
	}
}

// C: a failed fork must remove the stale PID file instead of leaving a
// dead PID behind.
func TestIssue552C_ForkFailureRemovesStalePIDFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePIDFile(pidPath, 999999, "sess", workDir); err != nil {
		t.Fatal(err)
	}

	orig := osStartProcess
	osStartProcess = func(string, []string, *os.ProcAttr) (*os.Process, error) {
		return nil, syscall.ENOENT
	}
	t.Cleanup(func() { osStartProcess = orig })

	if _, err := ForkIntoBackground("cfg", workDir, "sess"); err == nil {
		t.Fatal("expected fork failure")
	}
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Fatal("REGRESSION: stale PID file survived a failed fork (#552-C)")
	}
}

// E: CleanupDaemon must only remove a PID file owned by the current
// process; a foreground exit must not delete a background daemon's file.
func TestIssue552E_CleanupDaemonOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	// Foreign owner (a live daemon-like child) → file must survive.
	child := spawnDaemonLikeChild(t)
	t.Cleanup(func() { _ = child.Kill() })
	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	CleanupDaemon(workDir)
	if _, statErr := os.Stat(pidPath); os.IsNotExist(statErr) {
		t.Fatal("REGRESSION: foreground cleanup deleted the background daemon's PID file (#552-E)")
	}

	// Own PID → file removed.
	if err := WritePIDFile(pidPath, os.Getpid(), "sess", workDir); err != nil {
		t.Fatal(err)
	}
	CleanupDaemon(workDir)
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Fatal("own PID file should be removed")
	}

	// Corrupt file → removed (garbage).
	if err := os.WriteFile(pidPath, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	CleanupDaemon(workDir)
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Fatal("corrupt PID file should be removed")
	}

	// Missing file → no-op, no panic.
	CleanupDaemon(workDir)
}
