package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// Crash logging for main-goroutine panic containment (the 1a/1b follow-up to
// the v1.3.224 panic audit). safego protects every launched goroutine; what
// remained bare were the MAIN goroutines - the agent run loop and the CLI /
// desktop process entry points. A panic there killed the whole process with
// nothing on disk. These helpers turn that into: stack trace on disk under
// ~/.ggcode/crash/, plus an error returned to the caller (agent loop) or a
// readable stderr message (process entry).
//
// WriteCrashLog is deliberately lock-free and dependency-free beyond os and
// config paths: it runs during panic unwinding, where the process state is
// suspect - it must not touch mutexes, channels, or the agent itself.

const crashLogDir = "crash"

// WriteCrashLog persists a panic report for the given component and returns
// the file path. It captures the stack itself so callers stay one-liners
// and never need runtime/debug imports (cmd entry points already import
// this project's internal/debug package under the name "debug").
// Component names become filename prefixes ("agent", "cli", "desktop").
// Failures to write are swallowed and reported in the returned string - the
// caller is on a panic path and must not fail again.
func WriteCrashLog(component string, val any) string {
	dir := filepath.Join(config.ConfigDir(), crashLogDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Sprintf("<crash log not written: %v>", err)
	}
	name := fmt.Sprintf("%s-%s.log", component, time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("time:      %s\ncomponent: %s\npanic:     %v\n\n%s\n",
		time.Now().Format(time.RFC3339), component, val, debug.Stack())
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Sprintf("<crash log not written: %v>", err)
	}
	return path
}
