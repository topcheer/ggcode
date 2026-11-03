package restart

import (
	"fmt"
	"os"
	"os/exec"
)

func ExecSelf(binary string, args []string, env []string) error {
	cmd := exec.Command(binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", binary, err)
	}

	os.Exit(0)

	return nil
}
