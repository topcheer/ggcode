//go:build !windows

package session

import (
	"os"
	"syscall"
)

// lockIndexFile acquires a blocking exclusive flock on the index lock file.
// This serializes read-modify-write cycles across multiple ggcode processes
// that share the same session directory. The returned cleanup function
// releases the lock and closes the file descriptor.
func lockIndexFile(indexPath string) (func(), error) {
	lockPath := indexPath + ".flock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// Blocking exclusive lock — wait for other processes to finish their
	// read-modify-write cycle. The wait is typically sub-millisecond.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
