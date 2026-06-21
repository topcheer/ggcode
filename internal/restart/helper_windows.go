//go:build windows

package restart

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"golang.org/x/sys/windows"
)

// detachHelper starts the helper process in a new process group so it
// won't receive CTRL_BREAK_EVENT when the parent exits.
func detachHelper(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	return cmd.Start()
}

// waitForProcess polls until the given PID no longer exists.
func waitForProcess(pid int) error {
	deadline := time.Now().Add(30 * time.Second)
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Process already gone.
		return nil
	}
	defer windows.CloseHandle(handle)

	for time.Now().Before(deadline) {
		var exitCode uint32
		err := windows.GetExitCodeProcess(handle, &exitCode)
		if err != nil {
			return nil
		}
		if exitCode != 259 { // STILL_ACTIVE = 259
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for PID %d", pid)
}

// resetTerminal on Windows is a no-op — console mode is per-handle and
// the new process will set its own mode when it starts.
func resetTerminal() {
	// No-op on Windows.
}

// replaceBinary overwrites the target binary with the staged binary.
// On Windows the parent process has already exited, so the file is not
// locked and can be overwritten.
func replaceBinary(target, staged string) error {
	data, err := os.ReadFile(staged)
	if err != nil {
		return fmt.Errorf("read staged binary: %w", err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		return fmt.Errorf("write target binary: %w", err)
	}
	_ = os.Remove(staged)
	return nil
}

// launchTarget starts a new ggcode process and then exits the helper.
// On Windows, syscall.Exec is not available, so we start a new process
// that inherits the console.
func launchTarget(req HelperRequest) error {
	debug.Log("restart-helper", "launch: %s %v", req.Binary, req.Args)

	cmd := exec.Command(req.Binary, req.Args...)
	cmd.Dir = req.WorkDir
	cmd.Env = req.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new process: %w", err)
	}
	// Release the new process so it's not killed when the helper exits.
	_ = cmd.Process.Release()
	return nil
}
