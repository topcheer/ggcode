//go:build unix

package util

import (
	"os"
	"syscall"
)

func FileLock(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := acquireWithRetry(func() error {
		return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}); err != nil {
		f.Close()
		if err == errFileLockTimeout {
			return nil, os.NewSyscallError("flock(LOCK_EX|LOCK_NB)", err)
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
