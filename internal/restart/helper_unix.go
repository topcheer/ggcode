//go:build unix

package restart

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// detachHelper starts the helper process in a new session (setsid) so it
// is completely independent of the parent. The helper will not receive
// SIGHUP when the parent exits, and it can acquire the terminal after the
// parent releases it.
func detachHelper(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	return cmd.Start()
}

// waitForProcess polls until the given PID no longer exists.
// Timeout is 30 seconds.
func waitForProcess(pid int) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err != nil {
			// ESRCH = no such process
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for PID %d", pid)
}

// resetTerminal restores the terminal to a sane state after the parent
// process has exited. The parent's TUI restore may not have taken full
// effect because the process group teardown is asynchronous.
func resetTerminal() {
	// Run "stty sane" on the controlling terminal.
	// The helper inherited the terminal from the session, so /dev/tty
	// should be available.
	tty, err := os.Open("/dev/tty")
	if err != nil {
		debug.Log("restart-helper", "resetTerminal: cannot open /dev/tty: %v (continuing)", err)
		return
	}
	defer tty.Close()

	// stty sane resets cooked mode, echo, etc.
	stty := exec.Command("stty", "sane")
	stty.Stdin = tty
	stty.Stdout = tty
	stty.Stderr = nil
	_ = stty.Run()

	// Brief pause to let the terminal driver settle.
	time.Sleep(100 * time.Millisecond)
}

// replaceBinary overwrites the target binary with the staged binary.
// On Unix this is safe because the running process (the parent ggcode)
// has already exited — its mmapped pages are released.
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

// launchTarget uses syscall.Exec to replace the helper process with the
// new ggcode instance. This ensures the new process inherits the session
// leader role and terminal control established by the helper.
func launchTarget(req HelperRequest) error {
	debug.Log("restart-helper", "exec: %s %v", req.Binary, req.Args)

	// Open the terminal for the new process.
	// syscall.Exec preserves file descriptors, so we need to make sure
	// stdin/stdout/stderr are connected to the tty.
	tty, err := os.Open("/dev/tty")
	if err != nil {
		// Fallback: if /dev/tty is not available, use whatever stdin we have.
		tty = os.Stdin
	}

	// Ensure fd 0,1,2 point to the terminal.
	if tty.Fd() != 0 {
		syscall.Dup2(int(tty.Fd()), 0)
		syscall.Dup2(int(tty.Fd()), 1)
		syscall.Dup2(int(tty.Fd()), 2)
	}

	execArgs := append([]string{req.Binary}, req.Args...)
	return syscall.Exec(req.Binary, execArgs, req.Env)
}
