//go:build windows

package lsp

import (
	"os/exec"
	"syscall"
)

// detachConsole isolates a short-lived probe command from ggcode's console.
// On Windows, child console processes (npm.cmd, rustup, dotnet) inherit the
// parent console and node/cmd.exe call SetConsoleTitle while running - which
// steals the Windows Terminal tab title (observed as the tab showing
// "npm config get prefix" during LLM streaming, exactly when LSP discovery
// probes run). CREATE_NO_WINDOW gives the child no console at all, so it can
// neither flash a window nor touch our title. Output capture is unaffected:
// the callers use cmd.Output()/CombinedOutput() with pipes.
func detachConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
