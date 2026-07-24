//go:build !unix

package harness

import "os/exec"

// setProcessGroup is a no-op on non-Unix platforms (Windows).
// On Windows, child processes are automatically associated with a job object
// by the Go runtime when the parent is killed.
func setProcessGroup(cmd *exec.Cmd) {}

// signalProcessGroup is a no-op on Windows.
func signalProcessGroup(pid int) error { return nil }
