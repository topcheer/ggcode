//go:build windows

package mcp

import (
	"os/exec"
	"syscall"
)

func configureMCPCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	// CREATE_NO_WINDOW (0x08000000) prevents a console window from popping up
	// when spawning stdio MCP server processes on Windows.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
