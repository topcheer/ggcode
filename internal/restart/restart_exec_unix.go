//go:build unix

package restart

import (
	"fmt"
	"syscall"
)

// ExecSelf replaces the current process with a new invocation of the same binary.
// This is the Unix implementation using syscall.Exec which keeps the same PID
// and terminal control. Must be called after the TUI has released the terminal.
func ExecSelf(binary string, args []string, env []string) error {
	execArgs := append([]string{binary}, args...)
	err := syscall.Exec(binary, execArgs, env)
	if err != nil {
		return fmt.Errorf("exec %s: %w", binary, err)
	}
	// unreachable
	return nil
}
