//go:build windows

package daemon

import (
	"fmt"
	"os"
	"syscall"

	"github.com/topcheer/ggcode/internal/util"
)

func newBackgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func checkProcessAlive(proc *os.Process) error {
	if proc == nil || proc.Pid <= 0 {
		return fmt.Errorf("invalid process")
	}
	// os.FindProcess on Windows always succeeds even for dead PIDs,
	// so we must actively check liveness via OpenProcess.
	if !util.IsProcessAlive(proc.Pid) {
		return fmt.Errorf("process %d is not running", proc.Pid)
	}
	return nil
}
