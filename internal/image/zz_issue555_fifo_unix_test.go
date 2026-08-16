//go:build !windows

package image

import (
	"syscall"
)

// mkFifo creates a named pipe at path for the #555 FIFO regression tests.
func mkFifo(path string) error {
	return syscall.Mkfifo(path, 0600)
}
