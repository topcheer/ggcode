//go:build unix

package tui

import (
	"golang.org/x/sys/unix"
)

func fdGetFlags(fd int) (int, error) {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	return int(flags), err
}

func fdSetFlags(fd int, flags int) error {
	_, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
	return err
}
