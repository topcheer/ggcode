//go:build unix

package restart

import (
	"fmt"
	"syscall"
)

func ExecSelf(binary string, args []string, env []string) error {
	execArgs := append([]string{binary}, args...)
	err := syscall.Exec(binary, execArgs, env)
	if err != nil {
		return fmt.Errorf("exec %s: %w", binary, err)
	}

	return nil
}
