//go:build !windows

package update

import (
	"os/exec"
)

// launchElevated is a no-op on non-Windows platforms.
// On macOS/Linux, users who hit permission errors are instructed to run
// with sudo or reinstall to a user-writable location (handled in checkWritable).
func launchElevated(cmd *exec.Cmd) error {
	return cmd.Start()
}
