//go:build !windows

package tool

import "os/exec"

// On non-Windows platforms (Unix, js/wasm) this is a no-op. Unix process
// group handling is done in configureCommandCancellation (run_command_unix.go)
// via SysProcAttr{Setpgid: true}.
func applyCreateNoWindow(cmd *exec.Cmd) {}
