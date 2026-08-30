//go:build unix

package util

import (
	"os"
	"syscall"
)

// FileLock acquires a blocking exclusive cross-process lock on the given
// lock file path, serializing read-modify-write cycles between ggcode
// processes that share state files (cron stores, probe caches). The
// returned cleanup function releases the lock and closes the handle.
// AtomicWriteFile prevents torn files; FileLock prevents LOST UPDATES
// between concurrent full-file rewrites (#1337).
func FileLock(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
