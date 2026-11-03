//go:build windows

package lsp

import (
	"os/exec"
	"syscall"
)

func detachConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000,
	}
}
