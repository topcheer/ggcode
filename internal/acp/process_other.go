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

// configureACPCommandProcess sets up Windows process management for ACP
// agent subprocesses. #1298: this was an EMPTY function while mcp
// (#774) and plugin had already shipped the same fix - ACP agents are
// typically npm-distributed (cmd.exe shim -> node.exe grandchild), so
// Kill() only terminated the shim and the orphaned grandchild held the
// stdout pipe write-end, stalling cmd.Wait() indefinitely and leaking a
// process tree per reconnect.
func configureACPCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	// CREATE_NO_WINDOW: no console flash for headless agent processes.
	// CREATE_NEW_PROCESS_GROUP: group the tree so shutdown can target it.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	// Cancel tree-kills on context cancellation; WaitDelay bounds Wait()
	// even if a grandchild keeps the pipe write-end open after the kill.
	cmd.Cancel = func() error {
		return killACPProcess(cmd)
	}
	cmd.WaitDelay = acpCommandWaitDelay
}

// killACPProcess kills the whole process tree (taskkill /T walks it) and
// falls back to a plain Kill. Mirrors mcp's killProcessGroup (#774).
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
