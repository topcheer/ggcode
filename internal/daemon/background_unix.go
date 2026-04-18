//go:build unix

package daemon

import (
	"os"
	"syscall"
)

func newBackgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true, // create new process group
	}
}

func checkProcessAlive(proc *os.Process) error {
	return proc.Signal(syscall.Signal(0))
}
