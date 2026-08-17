package daemon

// Issue #552 characteristic tests: double-start guard wiring (A), /dev/null
// stdin for the daemonized child (B), PID-file ownership on cleanup (E),
// and stale PID file removal on fork failure (C).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spawnDaemonLikeChild starts a short-lived child whose identity carries a
// ggcode marker so daemonIdentityMatches accepts it as a daemon. This
// exercises the real identity-verification chain without a true ggcode fork.
// Cross-platform: /bin/sleep does not exist on Windows, so we re-exec the
// test binary itself from a copy named "ggcode-child" — argv[0] carries the
// "ggcode[" marker on Unix, and on Windows the process image name
// (identity_windows.go) matches the "ggcode-" prefix check.
func spawnDaemonLikeChild(t *testing.T) *os.Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	name := "ggcode-child"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	childPath := filepath.Join(t.TempDir(), name)
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	if err := os.WriteFile(childPath, data, 0o755); err != nil {
		t.Fatalf("copy test binary: %v", err)
	}
	proc, err := os.StartProcess(childPath,
		[]string{"ggcode[zz-issue552]", "-test.run=TestDaemonChildHelper"},
		// Windows requires explicit stdio handles in ProcAttr.Files (nil
		// yields "invalid argument" from CreateProcess); Unix would default
		// to /dev/null, but passing the parent's handles is fine for tests.
		&os.ProcAttr{
			Env:   append(os.Environ(), "GG_TEST_DAEMON_CHILD=1"),
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		},
	)
	if err != nil {
		t.Fatalf("spawn helper: %v (unsupported platform?)", err)
	}
	return proc
}

// TestDaemonChildHelper is not a real test: it is the entry point used when
// the test binary is re-executed as a daemon-like child by
// spawnDaemonLikeChild. It stays alive so the parent can observe a live
// ggcode-looking process; skipped instantly in normal test runs.
func TestDaemonChildHelper(t *testing.T) {
	if os.Getenv("GG_TEST_DAEMON_CHILD") != "1" {
		t.Skip("helper entry point; active only when re-executed as daemon child")
	}
	time.Sleep(60 * time.Second)
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
	makePIDFileUnreadable(t, pidPath)

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

	// Own PID → file removed. The previous WritePIDFile holds the lock (#574-D),
	// so we need to remove the file first to release it.
	_ = os.Remove(pidPath)
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
