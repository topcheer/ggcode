//go:build windows

package mcp

import (
	"os/exec"
	"strconv"
	"syscall"
)

func configureMCPCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
		return
	}
	_ = cmd.Process.Kill()
}
