//go:build windows

package util

import "syscall"

const errLockViolation = syscall.Errno(33)

func isLockBusy(err error) bool {
	return err == errLockViolation
}
