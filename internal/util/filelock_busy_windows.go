//go:build windows

package util

import "syscall"

// errLockViolation is ERROR_LOCK_VIOLATION (33): LockFileEx with
// LOCKFILE_FAIL_IMMEDIATELY reports it when the byte range is already
// locked - the retryable "busy" condition.
const errLockViolation = syscall.Errno(33)

// isLockBusy reports whether err means "lock held by someone else" (the
// retryable condition) as opposed to a permanent failure.
func isLockBusy(err error) bool {
	return err == errLockViolation
}
