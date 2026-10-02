//go:build windows

package runfile

// Regression probes for #3079 (process_windows.go):
//   V2a: exit code 259 is STILL_ACTIVE, but a process whose REAL exit code
//        was exactly 259 was judged alive forever (GetExitCodeProcess==259
//        ambiguity); the wait-state check is unambiguous.
//   V2b: procStartTime returned "" on Windows, fully disabling the #1624
//        PID-recycle fallback (runfile.go's gate is skipped when either
//        side has no token).
//
// Runs only on Windows CI; GOOS=windows compile/vet gates the rest.

import (
	"os"
	"testing"
)

// TestIssue3079_ExitCode259IsDead is THE V2a regression probe: a process
// that genuinely exited with code 259 must be reported dead.
func TestIssue3079_ExitCode259IsDead(t *testing.T) {
	p, err := os.StartProcess(`C:\Windows\System32\cmd.exe`, []string{`/c`, `exit 259`}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn cmd.exe: %v", err)
	}
	ws, err := p.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if ws.ExitCode() != 259 {
		t.Skipf("child exit code = %d, want 259 (cannot build the ambiguity case)", ws.ExitCode())
	}
	if processExists(p.Pid) {
		t.Fatal("process that exited with code 259 reported alive (V2a: STILL_ACTIVE ambiguity)")
	}
}

// TestIssue3079_ProcStartTimeToken covers V2b: non-empty for a live pid,
// stable across calls, empty for a dead pid.
func TestIssue3079_ProcStartTimeToken(t *testing.T) {
	self := os.Getpid()
	tok1 := procStartTime(self)
	if tok1 == "" {
		t.Fatal("procStartTime(self) empty - PID-recycle fallback still disabled (V2b)")
	}
	if tok2 := procStartTime(self); tok1 != tok2 {
		t.Fatalf("creation-time token unstable across calls: %q vs %q", tok1, tok2)
	}
	for _, r := range tok1 {
		if r < '0' || r > '9' {
			t.Fatalf("token not a plain decimal number: %q", tok1)
		}
	}

	p, err := os.StartProcess(`C:\Windows\System32\cmd.exe`, []string{`/c`, `exit`}, &os.ProcAttr{})
	if err != nil {
		t.Skipf("cannot spawn cmd.exe: %v", err)
	}
	_, _ = p.Wait()
	if got := procStartTime(p.Pid); got != "" {
		t.Fatalf("procStartTime(dead pid) = %q, want empty (V2b)", got)
	}
}

// TestIssue3079_SelfAlive pins the basics after the rewrite (companion of
// the #1290 suite).
func TestIssue3079_SelfAlive(t *testing.T) {
	if !processExists(os.Getpid()) {
		t.Fatal("current process must be reported alive via wait-state check")
	}
	if processExists(0) {
		t.Fatal("pid 0 must be dead")
	}
}
