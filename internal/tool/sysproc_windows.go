//go:build windows

package tool

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func applyCreateNoWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
