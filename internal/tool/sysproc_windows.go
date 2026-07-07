//go:build windows

package tool

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW prevents the child process from flashing a console
// window when the parent is a GUI application (e.g. Wails desktop). On
// Windows the flag also gives the child its own hidden console buffer
// instead of inheriting the parent's visible terminal, reducing the
// surface for WriteConsole-based output that bypasses Go's stdout/stderr
// pipe redirection.
//
// We deliberately avoid DETACHED_PROCESS (0x00000008) because it removes
// the console entirely and breaks programs that call console APIs.
const createNoWindow = 0x08000000

func applyCreateNoWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
