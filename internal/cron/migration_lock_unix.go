//go:build unix

package cron

import (
	"os"
	"syscall"
)

// acquireMigrationLock takes a non-blocking exclusive flock on path.
// Returns a release func and true on success; (nil, false) when another
// process holds the lock (#414).
func acquireMigrationLock(path string) (func(), bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false // cannot create lock file — fall back to unlocked path
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}
