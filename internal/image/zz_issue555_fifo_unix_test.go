//go:build !windows

package image

import (
	"syscall"
)

func mkFifo(path string) error {
	return syscall.Mkfifo(path, 0600)
}
