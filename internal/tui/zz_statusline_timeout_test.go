package tui

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestStatuslineGrandchildPipeRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	start := time.Now()
	got, ok := runStatuslineCommand(`sleep 5 & sleep 5`, statuslinePayload{}, 80*time.Millisecond)
	elapsed := time.Since(start)
	if ok || got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("grandchild held pipe: took %s, want <1.5s", elapsed)
	}
	t.Logf("elapsed=%s", elapsed)
}

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
		t.Fatalf("got %q, want empty", got)
	}

	if elapsed > 2*time.Second {
		t.Fatalf("session-escaped survivor held pipe: took %s, want <2s", elapsed)
	}
	t.Logf("elapsed=%s", elapsed)
}
