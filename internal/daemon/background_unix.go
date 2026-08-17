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

// flockNonBlocking acquires a non-blocking exclusive advisory lock on f,
// held for the lifetime of the file handle. Returns EAGAIN-equivalent
// errors when another process holds the lock.
func flockNonBlocking(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func checkProcessAlive(proc *os.Process) error {
	return proc.Signal(syscall.Signal(0))
}
