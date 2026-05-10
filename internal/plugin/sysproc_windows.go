//go:build windows

package plugin

import "os/exec"

// On Windows, CommandContext already handles cancellation correctly via
// TerminateProcess, so no process-group setup is needed.
func setupProcessGroupCancel(_ *exec.Cmd) {}
