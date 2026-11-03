package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

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

func TestDaemonChildHelper(t *testing.T) {
	if os.Getenv("GG_TEST_DAEMON_CHILD") != "1" {
		t.Skip("helper entry point; active only when re-executed as daemon child")
	}
	time.Sleep(60 * time.Second)
}

func TestIssue552A_EnsureDaemonSlotRejectsLiveDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

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

func TestIssue552B_ForkStdinIsDevNull(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	var gotStdinName string
	orig := osStartProcess
	osStartProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		if len(attr.Files) > 0 {

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

func TestIssue552E_CleanupDaemonOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	pidPath, err := PIDFilePath(workDir)
	if err != nil {
		t.Fatal(err)
	}

	child := spawnDaemonLikeChild(t)
	t.Cleanup(func() { _ = child.Kill() })
	if err := WritePIDFile(pidPath, child.Pid, "sess", workDir); err != nil {
		t.Fatal(err)
	}
	CleanupDaemon(workDir)
	if _, statErr := os.Stat(pidPath); os.IsNotExist(statErr) {
		t.Fatal("REGRESSION: foreground cleanup deleted the background daemon's PID file (#552-E)")
	}

	_ = os.Remove(pidPath)
	if err := WritePIDFile(pidPath, os.Getpid(), "sess", workDir); err != nil {
		t.Fatal(err)
	}
	CleanupDaemon(workDir)
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Fatal("own PID file should be removed")
	}

	if err := os.WriteFile(pidPath, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	CleanupDaemon(workDir)
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Fatal("corrupt PID file should be removed")
	}

	CleanupDaemon(workDir)
}
