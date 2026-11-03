//go:build !windows

package swarm

import (
	"os"
	"syscall"
	"time"
)

const teammateExpLockDeadline = 3 * time.Second

func lockTeammateExpFile(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(teammateExpLockDeadline)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, os.ErrDeadlineExceeded
		}
		time.Sleep(50 * time.Millisecond)
	}
}
