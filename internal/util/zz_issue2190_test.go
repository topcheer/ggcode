//go:build !windows

package util

import (
	"os"
	"syscall"
	"testing"
)

func TestIsProcessAliveBasicSemantics(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("self must be alive")
	}

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

	for i := 0; i < 100; i++ {
		if !IsProcessAlive(pid) {
			return
		}
		syscall.Wait4(pid, nil, syscall.WNOHANG, nil)
	}
	t.Fatal("reaped child must be reported dead")
}
