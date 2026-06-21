package restart

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
)

// ExecRestart replaces the current process in-place using syscall.Exec.
// No child process is spawned — the same PID continues with a fresh image.
// This avoids the orphaned process group problem that causes EIO on
// terminal raw mode entry.
//
// For /update: the caller must call update.Service.ApplyBinary() BEFORE
// calling this function. ApplyBinary atomically replaces the binary files
// (temp-file + rename). On Unix the running process holds the old inode,
// so syscall.Exec then loads the new file at the same path.
//
// Must be called AFTER the TUI has released the terminal (program.Run()
// has returned and terminal is restored to cooked mode).
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

// ResolveBinary returns the path to the ggcode binary.
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
