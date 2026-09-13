//go:build unix

package util

import (
	"os"
	"syscall"
)

// FileLock acquires a bounded exclusive cross-process lock on the given
// lock file path, serializing read-modify-write cycles between ggcode
// processes that share state files (cron stores, probe caches). The
// returned cleanup function releases the lock and closes the handle.
// AtomicWriteFile prevents torn files; FileLock prevents LOST UPDATES
// between concurrent full-file rewrites (#1337).
//
// #1834 case 2: acquisition is NON-blocking (LOCK_NB) with a bounded retry
// loop instead of a bare blocking flock. A blocking flock hung forever
// when the holder was alive-but-slow (SIGSTOP, giant merge, suspended
// desktop). On timeout the error is returned; every caller degrades to
// its unlocked merge-and-write path (fail-open, same as the wasm stub).
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
