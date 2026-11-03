//go:build unix

package hooks

import (
	"os/exec"
	"syscall"
)

func setProcessGroupKill(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	prev := c.Cancel
	c.Cancel = func() error {
		if c.Process != nil {

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
