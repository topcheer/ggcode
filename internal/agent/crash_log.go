package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	// Sanitize the component into a filename fragment: it is an exported API
	// and a "../../x" component would otherwise escape the crash dir.
	component = filepath.Base(filepath.Clean(component))
	dir := filepath.Join(config.ConfigDir(), crashLogDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Sprintf("<crash log not written: %v>", err)
	}
	// #1616-B: second-resolution names collide on same-second double
	// crashes (agent recover does NOT exit; retry loops re-panic) - the
	// O_TRUNC write silently destroyed the FIRST crash scene, exactly the
	// one comparison of consecutive crashes needs. Nanosecond suffix.
	now := time.Now()
	name := fmt.Sprintf("%s-%s-%09d.log", component, now.Format("20060102-150405"), now.Nanosecond())
	path := filepath.Join(dir, name)
	// Guard the %v formatting: a panic value whose String()/Error() itself
	// panics would re-panic HERE, inside this recover-adjacent helper, and
	// replace the original panic - losing containment entirely.
	panicText := func() (out string) {
		defer func() {
			if recover() != nil {
				out = fmt.Sprintf("<unprintable %T>", val)
			}
		}()
		return fmt.Sprintf("%v", val)
	}()
	// Cap the stack: a runaway recursion recovered late can produce a
	// multi-hundred-MB dump that would OOM the crash path itself.
	stack := debug.Stack()
	const maxStack = 1 << 20 // 1 MiB
	if len(stack) > maxStack {
		stack = stack[:maxStack]
	}
	// Full goroutine dump: the panicking goroutine's stack alone often hides
	// the cause (e.g. a TUI event-loop stall shows up as innocent goroutines
	// blocked on tea.Program.Send while the real blocker sits in an Update
	// handler). Captured from runtime.Stack(all=true) with a generous but
	// bounded cap; written AFTER the primary stack so the most important
	// frames stay at the top of the file.
	const maxAllStack = 8 << 20 // 8 MiB
	allBuf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(allBuf, true)
		if n < len(allBuf) {
			allBuf = allBuf[:n]
			break
		}
		if len(allBuf) >= maxAllStack {
			// #1616-A: runtime.Stack walks allgs in creation order - the
			// NEWEST goroutines (often the culprit in a just-spawned task)
			// sit at the tail and a silent truncate drops exactly them.
			// Mark the truncation so log readers know the dump is partial.
			allBuf = append(allBuf[:maxAllStack], []byte(fmt.Sprintf("\n[all-goroutine dump truncated at %d bytes; newest goroutines dropped]\n", maxAllStack))...)
			break
		}
		allBuf = make([]byte, len(allBuf)*2)
	}
	body := fmt.Sprintf("time:      %s\ncomponent: %s\npanic:     %s\n\n%s\n\n=== all goroutines ===\n%s\n",
		time.Now().Format(time.RFC3339), component, panicText, stack, allBuf)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Sprintf("<crash log not written: %v>", err)
	}
	return path
}
