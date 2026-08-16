package runfile

// Issue #549 bug F: liveness was checked only via signal 0, so a port file
// whose PID was recycled by an unrelated live process never expired. The
// mtime fallback removes port files older than staleAfter (24h) even when
// the PID appears alive.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// deadChildPID spawns a child process that exits immediately and returns its
// now-dead PID, or 0 if that could not be determined.
func deadChildPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		return 0
	}
	return cmd.ProcessState.Pid()
}

func writeTestPortFile(t *testing.T, dir string, pid int, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, "session-test.json")
	pf := PortFile{
		Addr:      "127.0.0.1:19999",
		Token:     "tok",
		PID:       pid,
		SessionID: "session-test",
		Workspace: dir,
		Mode:      "auto",
	}
	data, err := json.Marshal(pf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	return p
}

func TestIssue549BugF_PIDReuseStaleFileExpires(t *testing.T) {
	dir := t.TempDir()
	// PID is the *test process itself* — alive by signal-0 semantics, but
	// unrelated to the (recycled) session that wrote the file. The file's
	// mtime is 25h old, so it must be treated as stale and removed.
	p := writeTestPortFile(t, dir, os.Getpid(), 25*time.Hour)

	if _, err := readAtPath(p); err == nil {
		t.Fatal("expected stale error for old port file with live-but-recycled PID")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stale port file should have been removed")
	}
}

func TestIssue549BugF_FreshFileWithLivePIDStillValid(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPortFile(t, dir, os.Getpid(), 0)

	pf, err := readAtPath(p)
	if err != nil {
		t.Fatalf("fresh port file with live PID must remain valid: %v", err)
	}
	if pf.PID != os.Getpid() || pf.SessionID != "session-test" {
		t.Fatalf("unexpected port file content: %+v", pf)
	}
}

func TestIssue549BugF_DeadPIDStillRemoved(t *testing.T) {
	dir := t.TempDir()
	// PID 1 is init on unix: never a ggcode instance. Use a PID that is
	// essentially guaranteed dead — a just-reaped child.
	dead := deadChildPID(t)
	if dead == 0 {
		t.Skip("could not obtain a dead PID on this platform")
	}
	p := writeTestPortFile(t, dir, dead, 0)
	if _, err := readAtPath(p); err == nil {
		t.Fatal("expected stale error for dead PID")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stale port file for dead PID should have been removed")
	}
}
