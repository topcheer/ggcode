//go:build windows

package plugin

import (
	"os/exec"
	"strconv"
	"time"
)

func setupProcessGroupCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 3 * time.Second
}
