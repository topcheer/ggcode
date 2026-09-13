package util

import (
	"errors"
	"time"
)

// fileLockTimeout bounds how long FileLock retries a busy lock before
// giving up (#1834 case 2). Var (not const) so tests can shrink it; see
// filelock_test.go. The previous blocking acquisition (flock without
// LOCK_NB, LockFileEx without FAIL_IMMEDIATELY) hung forever when the
// holder was alive-but-slow (SIGSTOP, giant-file merge, suspended
// desktop), wedging the other process's save path. Every caller degrades
// gracefully on error (proceeds unlocked - same as the wasm stub), so a
// timeout error is strictly better than an indefinite hang.
var fileLockTimeout = 10 * time.Second

// FileLockTimeout exposes the bound for callers that want to log it.
func FileLockTimeout() time.Duration { return fileLockTimeout }

// fileLockRetryInterval paces the non-blocking acquisition attempts.
const fileLockRetryInterval = 10 * time.Millisecond

// errFileLockTimeout is returned (wrapped by the platform layers) when the
// lock could not be acquired within FileLockTimeout.
var errFileLockTimeout = errors.New("file lock busy: holder did not release within timeout")

// acquireWithRetry runs try() in a tight retry loop until it succeeds,
// fails permanently, or the deadline passes. It returns the try() error on
// permanent failure, or errFileLockTimeout when the deadline expires.
// Shared by the unix (flock LOCK_NB) and windows (LockFileEx
// FAIL_IMMEDIATELY) implementations.
func acquireWithRetry(try func() error) error {
	deadline := time.Now().Add(fileLockTimeout)
	for {
		err := try()
		if err == nil {
			return nil
		}
		if !isLockBusy(err) {
			return err
		}
		if time.Now().After(deadline) {
			return errFileLockTimeout
		}
		time.Sleep(fileLockRetryInterval)
	}
}
