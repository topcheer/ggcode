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
	// CREATE_NO_WINDOW prevents console window flashing (GUI mode) and
	// gives the child process a new hidden console instead of inheriting
	// the parent's visible terminal. This reduces the surface for
	// WriteConsole-based output that bypasses Go's stdout/stderr pipes.
	applyCreateNoWindow(cmd)
}
