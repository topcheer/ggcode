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

// openPIDFile opens the PID file for read/write, creating it if missing.
func openPIDFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
}

func checkProcessAlive(proc *os.Process) error {
	// #1535: EPERM means the process EXISTS but is not ours (root-owned
	// daemon checked by a regular user, cross-user on shared hosts).
	// Returning it as an error made CheckExistingDaemon treat the live
	// daemon as gone, delete the PID file, and fork a second daemon.
	// EPERM = alive; only ESRCH means gone.
	err := proc.Signal(syscall.Signal(0))
	if err == syscall.EPERM {
		return nil
	}
	return err
}
