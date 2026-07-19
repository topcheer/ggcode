//go:build windows

package agent

import "os/exec"

// configureVerifyCommand sets up process-group isolation for verify
// commands. On Windows, the default exec.CommandContext cancel behavior
// (TerminateProcess) is sufficient since Windows doesn't have process
// groups in the Unix sense.
func configureVerifyCommand(cmd *exec.Cmd) {
	// No-op: Go's exec package handles process termination on Windows.
}
