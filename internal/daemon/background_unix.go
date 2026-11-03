//go:build unix

package daemon

import (
	"os"
	"syscall"
)

func newBackgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func flockNonBlocking(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func openPIDFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
}

func checkProcessAlive(proc *os.Process) error {

	err := proc.Signal(syscall.Signal(0))
	if err == syscall.EPERM {
		return nil
	}
	return err
}
