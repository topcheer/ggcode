//go:build unix

package util

import (
	"errors"
	"syscall"
)

// isLockBusy reports whether err means "lock held by someone else" (the
// retryable condition) as opposed to a permanent failure.
func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR)
}
