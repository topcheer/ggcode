//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// lockTrajFile takes an exclusive cross-process flock on lockPath,
// creating it if needed. The returned unlock func must be called
// (defer). #1512 case C: the trajectory JSONL is workspace-shared
// across Agent instances and processes; the in-memory s.mu cannot
// serialize them.
func lockTrajFile(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
