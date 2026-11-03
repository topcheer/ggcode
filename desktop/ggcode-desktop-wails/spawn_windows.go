//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow prevents the OS from flashing a console window when a
// GUI-subsystem process (wails -H windowsgui) spawns a console-subsystem
// child (powershell, tray helper, installers). Without CREATE_NO_WINDOW every
// such spawn opens a visible terminal briefly.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
