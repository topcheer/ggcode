//go:build !windows

package util

// #2190 regression: kill(pid,0) EPERM means the process EXISTS but is
// not signalable by us (POSIX) - it was returned as dead, the Unix twin
// of the #1723 Windows bug (false-dead deletes PID files and forks
// second daemons / concurrent crash recovery). The EPERM branch itself
// needs a cross-privilege process (not unit-testable); these pin the
// surrounding semantics that must not regress alongside it.

import (
	"os"
	"syscall"
	"testing"
)

func TestIsProcessAliveBasicSemantics(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("self must be alive")
	}
	// A definitely-dead PID: spawn and reap one.
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	pid, err := syscall.ForkExec("/bin/true", []string{"/bin/true"}, &syscall.ProcAttr{Files: []uintptr{r.Fd(), w.Fd(), w.Fd()}})
	if err != nil {
		t.Skipf("forkexec: %v", err)
	}
	// Wait for exit; probe until dead (bounded).
	for i := 0; i < 100; i++ {
		if !IsProcessAlive(pid) {
			return // dead, as expected
		}
		syscall.Wait4(pid, nil, syscall.WNOHANG, nil)
	}
	t.Fatal("reaped child must be reported dead")
}
