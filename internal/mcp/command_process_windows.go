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
	// CREATE_NO_WINDOW (0x08000000) prevents a console window from popping up
	// when spawning stdio MCP server processes on Windows.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

// killProcessGroup kills the process tree on Windows.
// #774: cmd.Process.Kill() only terminates the direct child. stdio MCP
// servers are usually npx/uvx wrapper shims -- the real node.exe/python.exe
// is a grandchild that survived every kill and leaked on each reconnect,
// holding the pipe write-end and stalling teardown. taskkill /T walks the
// tree and is built into every Windows; fall back to Kill if it fails.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
		return
	}
	_ = cmd.Process.Kill()
}
