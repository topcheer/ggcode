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

// detachHelper simply starts the helper process. We intentionally do NOT
// use Setsid or Setpgid — the helper must stay in the same session and
// foreground process group so it can access the terminal (tcsetattr) after
// the parent exits. If we put it in a different PG (Setpgid), the terminal's
// foreground PG reverts to the shell when the parent exits, and the helper
// gets EIO when trying to enter raw mode.
//
// The helper inherits the parent's stdio (terminal fds) and does
// syscall.Exec to become the new ggcode, all within the same PG.
func detachHelper(cmd *exec.Cmd) error {
	// No SysProcAttr — stay in the same process group and session.
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
	// stty sane resets cooked mode, echo, etc.
	// The helper inherited stdin/stdout from the parent, which is the
	// terminal — use it directly instead of opening /dev/tty.
	stty := exec.Command("stty", "sane")
	stty.Stdin = os.Stdin
	stty.Stdout = os.Stdout
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
// new ggcode instance. Since the helper inherited the terminal stdio from
// the parent, fds 0/1/2 are already connected to the terminal — no dup2
// needed. syscall.Exec preserves them automatically.
func launchTarget(req HelperRequest) error {
	debug.Log("restart-helper", "exec: %s %v", req.Binary, req.Args)

	// Change to the working directory before exec.
	if req.WorkDir != "" {
		_ = os.Chdir(req.WorkDir)
	}

	execArgs := append([]string{req.Binary}, req.Args...)
	return syscall.Exec(req.Binary, execArgs, req.Env)
}
