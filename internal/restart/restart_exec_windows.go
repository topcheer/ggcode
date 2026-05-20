package restart

import (
	"fmt"
	"os"
	"os/exec"
)

// ExecSelf replaces the current process with a new invocation of the same binary.
// On Windows, syscall.Exec is not available, so we start a new process and exit.
// Must be called after the TUI has released the terminal.
func ExecSelf(binary string, args []string, env []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", binary, err)
	}
	// Exit the current process; the new one takes over the terminal.
	os.Exit(0)
	// unreachable
	return nil
}
