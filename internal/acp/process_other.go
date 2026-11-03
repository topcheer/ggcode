//go:build !unix

package acp

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const acpCommandWaitDelay = 3 * time.Second

func configureACPCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	cmd.Cancel = func() error {
		return killACPProcess(cmd)
	}
	cmd.WaitDelay = acpCommandWaitDelay
}

func killACPProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !execIsProcessDone(err) {
		return err
	}
	return nil
}

func execIsProcessDone(err error) bool {
	return err == nil || err == os.ErrProcessDone
}
