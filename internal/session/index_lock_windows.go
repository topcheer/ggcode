//go:build windows

package session

import (
	"os"
	"syscall"
)

// lockIndexFile acquires a blocking exclusive lock on the index lock file.
// This serializes read-modify-write cycles across multiple ggcode processes
// that share the same session directory. The returned cleanup function
// releases the lock and closes the file handle.
func lockIndexFile(indexPath string) (func(), error) {
	lockPath := indexPath + ".flock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// Blocking exclusive lock (no LOCKFILE_FAIL_IMMEDIATELY).
	const lockfileExclusiveLock = 0x00000002
	if err := lockFileEx(syscall.Handle(f.Fd()), lockfileExclusiveLock, 0, 1, 0); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unlockFileEx(syscall.Handle(f.Fd()), 1, 0)
		f.Close()
	}, nil
}
