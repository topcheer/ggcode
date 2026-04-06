//go:build !unix

package tool

import (
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

func configureCommandCancellation(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		return
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 750 * time.Millisecond
}
