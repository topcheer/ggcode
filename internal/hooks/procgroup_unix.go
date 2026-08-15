//go:build unix

package hooks

import (
	"os/exec"
	"syscall"
)

// setProcessGroupKill puts the hook command in its own process group and
// overrides the exec.CommandContext cancellation so a timeout kills the
// entire group (negative pid), not just the shell. Without this, hook
// commands spawning background children (`cmd &`) leave those children
// adopted by init after the timeout fires (#413).
func setProcessGroupKill(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	prev := c.Cancel
	c.Cancel = func() error {
		if c.Process != nil {
			// SIGKILL the process group; fall back to killing the shell
			// directly if the group is already gone (ESRCH).
			if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
				_ = c.Process.Kill()
			}
		}
		if prev != nil {
			_ = prev()
		}
		return nil
	}
}
