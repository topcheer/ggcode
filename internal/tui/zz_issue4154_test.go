//go:build unix

package tui

import (
	"os"
	"testing"
)

// #4154: the guard writer is the read side of stdoutDeadFlag - while the
// monitor flags stdout dead, frames must be dropped instead of blocking on
// the dead pty; once healthy again writes must pass through.
func TestStdoutGuardWriterDropAndPassthrough(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "guard*.out")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer f.Close()
	w := stdoutGuardWriter{File: f}

	// Dead: write is swallowed, underlying file untouched.
	stdoutDeadFlag.Store(true)
	n, err := w.Write([]byte("DROPPED"))
	if err != nil || n != 7 {
		t.Fatalf("dead write: n=%d err=%v", n, err)
	}
	if fi, _ := f.Stat(); fi.Size() != 0 {
		t.Fatalf("dead write leaked %d bytes to underlying writer", fi.Size())
	}

	// Alive: passes through.
	stdoutDeadFlag.Store(false)
	if _, err := w.Write([]byte("PASSED")); err != nil {
		t.Fatalf("alive write: %v", err)
	}
	buf, _ := os.ReadFile(f.Name())
	if string(buf) != "PASSED" {
		t.Fatalf("alive write not passed through: %q", buf)
	}
}

// #4154 latent half: a monitor that dies while stdout is dead must not
// leave the global flag stuck at true - the stop func resets it, otherwise
// the next tea program in-process would drop frames forever.
func TestStdoutHealthMonitorStopResetsDeadFlag(t *testing.T) {
	if isTerminalStdout() {
		t.Skip("needs piped stdout (test harness)")
	}
	stdoutDeadFlag.Store(true)
	stop := startStdoutHealthMonitor(t.Context(), nil)
	stop()
	if stdoutDeadFlag.Load() {
		t.Fatalf("stop() must reset stdoutDeadFlag")
	}
}
