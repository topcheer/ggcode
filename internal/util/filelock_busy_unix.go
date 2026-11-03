//go:build unix

package util

import (
	"errors"
	"syscall"
)

func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR)
}
