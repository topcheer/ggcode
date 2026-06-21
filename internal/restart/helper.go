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
// For /update: stagedBinary points to the downloaded new version.
// We atomically swap the binary file before exec (rename old → write new).
// On Unix this works because the running process holds the old inode;
// syscall.Exec then loads the new file at the same path.
//
// Must be called AFTER the TUI has released the terminal (program.Run()
// has returned and terminal is restored to cooked mode).
func ExecRestart(binary string, args []string, env []string, stagedBinary string) error {
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

	// For /update: swap the binary file before exec.
	if stagedBinary != "" {
		if err := swapBinary(binary, stagedBinary); err != nil {
			return fmt.Errorf("swap binary: %w", err)
		}
		debug.Log("restart", "binary swapped: %s <- %s", binary, stagedBinary)
	}

	debug.Log("restart", "exec: %s %v", binary, args)
	return ExecSelf(binary, args, env)
}

// swapBinary atomically replaces the target binary with the staged version.
// On Unix, the running binary's inode is preserved by the kernel even after
// the directory entry is renamed, so this is safe to call on the currently
// executing binary.
func swapBinary(target, staged string) error {
	// Try a direct rename first (atomic on Unix).
	if err := os.Rename(staged, target); err != nil {
		// Fall back to write + rename via temp file.
		data, err := os.ReadFile(staged)
		if err != nil {
			return fmt.Errorf("read staged binary: %w", err)
		}
		tmp := target + ".new"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return fmt.Errorf("write temp binary: %w", err)
		}
		_ = os.Chmod(tmp, 0o755)
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("rename temp binary: %w", err)
		}
	}
	return nil
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
