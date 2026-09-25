package tui

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// Probe: grandchild holding the pipe must not extend Output() past the
// timeout. `sh -c 'sleep 5 & sleep 5'` forces a forked grandchild.
func TestStatuslineGrandchildPipeRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	start := time.Now()
	got, ok := runStatuslineCommand(`sleep 5 & sleep 5`, statuslinePayload{}, 80*time.Millisecond)
	elapsed := time.Since(start)
	if ok || got != "" {
		t.Fatalf("got (%q, %v), want empty/false", got, ok)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("grandchild held pipe: took %s, want <1.5s", elapsed)
	}
	t.Logf("elapsed=%s", elapsed)
}

// Probe: a survivor that escapes the process group (own session via
// setsid(2)) is missed by the group kill but must still be cut off by the
// WaitDelay pipe-release backstop instead of blocking Output() for its full
// lifetime. python3 is used for the escape because the setsid(1) binary does
// not exist on macOS runners.
func TestStatuslineWaitDelayBackstop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	escape := `python3 -c 'import os,time; os.setsid(); time.sleep(5)' & sleep 5`
	start := time.Now()
	got, ok := runStatuslineCommand(escape, statuslinePayload{}, 80*time.Millisecond)
	elapsed := time.Since(start)
	if ok || got != "" {
		t.Fatalf("got (%q, %v), want empty/false", got, ok)
	}
	// WaitDelay is 1s: expect ~1.1s total, well under the 2s tolerance the
	// other timeout tests use. Without the backstop this blocks ~5s.
	if elapsed > 2*time.Second {
		t.Fatalf("session-escaped survivor held pipe: took %s, want <2s", elapsed)
	}
	t.Logf("elapsed=%s", elapsed)
}
