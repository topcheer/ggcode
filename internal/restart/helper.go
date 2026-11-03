package restart

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
)

func ExecRestart(binary string, args []string, env []string) error {
	if binary == "" {
		b, err := ResolveBinary()
		if err != nil {
			return fmt.Errorf("resolve binary: %w", err)
		}
		binary = b
	}
	if len(env) == 0 {
		env = os.Environ()
	}

	debug.Log("restart", "exec: %s %v", binary, args)
	return ExecSelf(binary, args, env)
}

func ResolveBinary() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return execPath, nil
	}
	return resolved, nil
}
