//go:build windows

package plugin

import (
	"os/exec"
	"strconv"
	"time"
)

// On Windows, exec.CommandContext's default cancel only terminates the
// direct child process. Command plugins are frequently wrapper shims
// (sh.exe, npx.cmd, ...) whose real work runs in a grandchild that
// survives cancellation, inherits the stdout pipe write-end, and stalls
// io.ReadAll until it exits (same defect class as mcp #774). Mirror the
// unix sysproc_unix.go semantics: on cancel, taskkill /T /F walks and
// kills the whole tree (falling back to Kill); WaitDelay guarantees
// Wait can never hang indefinitely even if the tree kill fails.
func setupProcessGroupCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 3 * time.Second
}
