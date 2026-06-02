//go:build unix

package acp

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

const acpCommandWaitDelay = 3 * time.Second

func configureACPCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return killACPProcess(cmd)
	}
	cmd.WaitDelay = acpCommandWaitDelay
}

func killACPProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil && pgid > 0 {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil || err == syscall.ESRCH {
			return nil
		}
	}
	if err := cmd.Process.Kill(); err != nil && !errorsIsProcessDone(err) {
		return err
	}
	return nil
}

func errorsIsProcessDone(err error) bool {
	return err == nil || err == os.ErrProcessDone
}
