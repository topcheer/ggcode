//go:build windows

package tool

import (
	"syscall"
)

const detatchedProcess = 0x00000008
const createNewProcessGroup = 0x00000200

func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: detatchedProcess | createNewProcessGroup,
	}
}
