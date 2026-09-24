package tui

import (
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
	got := runStatuslineCommand(`sleep 5 & sleep 5`, statuslinePayload{}, 80*time.Millisecond)
	elapsed := time.Since(start)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("grandchild held pipe: took %s, want <1.5s", elapsed)
	}
	t.Logf("elapsed=%s", elapsed)
}
