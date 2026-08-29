package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
)

// logWriter redirects standard library log output to debug.Log so that
// third-party libraries (pion/turn, pion/webrtc) writing via the standard
// log package don't corrupt the TUI by writing directly to stderr.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	debug.Log("stderr", "%s", string(p))
	return len(p), nil
}

// redirectStderr replaces os.Stderr with a pipe that captures all writes
// and routes them to debug.Log. This is the bulletproof approach:
// even if a library creates its own log.New(os.Stderr, ...) or writes
// via fmt.Fprint(os.Stderr, ...), the output is captured instead of
// corrupting the TUI.
//
// The original stderr is saved as origStderr for use during shutdown
// (e.g. fatal error messages that must reach the user).
var origStderr *os.File

func redirectStderr() {
	origStderr = os.Stderr

	r, w, err := os.Pipe()
	if err != nil {
		// If pipe creation fails, fall back to /dev/null — better than
		// corrupting the TUI.
		if devNull, err2 := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err2 == nil {
			os.Stderr = devNull
		}
		return
	}

	os.Stderr = w

	var mu sync.Mutex
	buf := make([]byte, 0, 4096)
	go func() {
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf = append(buf, tmp[:n]...)
				// Flush complete lines to debug.Log
				for {
					idx := -1
					for i, b := range buf {
						if b == '\n' {
							idx = i
							break
						}
					}
					if idx < 0 {
						break
					}
					line := string(buf[:idx])
					buf = buf[idx+1:]
					if line != "" {
						debug.Log("stderr", "%s", line)
					}
				}
				mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()
}

// shouldRedirectStderr reports whether os.Stderr should be captured into the
// debug log for this invocation. The redirect exists solely to protect the
// TUI from stderr writes corrupting the drawing surface. Pipe mode
// (`ggcode -p`) and non-TUI subcommands (`version`, `daemon`, `im`, ...) never
// render the TUI, so redirecting buys nothing and actively swallows every
// error message: RunPipe's fmt.Fprintf(os.Stderr, ...) paths and its os.Exit
// calls happen deep inside cobra's RunE, where the restore-before-exit logic
// below is structurally unreachable (#536). Skipping the redirect up front
// keeps CI/scripts seeing real error output on stderr.
func shouldRedirectStderr(args []string) bool {
	for _, a := range args {
		if a == "-p" || a == "--prompt" || strings.HasPrefix(a, "--prompt=") {
			return false // pipe mode: errors must reach the caller's stderr
		}
	}
	// A bare first argument that is not a flag is a subcommand
	// (version, completion, daemon, im, mcp, llm-probe, acp, ...) — no TUI.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return false
	}
	return true
}

func main() {
	// Top-level panic containment (1a of the v1.3.224 crash follow-up):
	// every launched goroutine is safego-protected, but this main goroutine
	// (cobra execution, TUI event loop via tea.Run) was bare - a panic here
	// died with nothing on disk. Registered BEFORE defer debug.Close() so it
	// unwinds first (LIFO): write the crash log, restore stderr, exit
	// nonzero. os.Exit skips remaining defers by design on a crash path.
	defer func() {
		if r := recover(); r != nil {
			path := agent.WriteCrashLog("cli", r)
			// Flush the debug ring BEFORE exiting: os.Exit skips the
			// defer debug.Close() below (LIFO: this defer runs first), and the
			// ring holds the last pre-panic log lines - the most valuable
			// diagnostics. close() is idempotent, so the deferred one after is
			// harmless.
			debug.Close()
			if origStderr != nil {
				os.Stderr = origStderr
			}
			fmt.Fprintf(os.Stderr, "ggcode crashed: %v\npanic log: %s\nPlease report this with the log file if it repeats.\n", r, path)
			os.Exit(1)
		}
	}()
	defer debug.Close()

	// Redirect os.Stderr at the file descriptor level. This catches ALL
	// writes to stderr regardless of how the library obtained the fd:
	// log.New(os.Stderr, ...), fmt.Fprint(os.Stderr, ...), etc.
	// #536: skipped entirely for pipe mode and non-TUI subcommands so their
	// error output reaches the real stderr instead of the debug ring buffer.
	if shouldRedirectStderr(os.Args[1:]) {
		redirectStderr()

		// Also redirect the standard log package's default output.
		log.SetOutput(logWriter{})
		log.SetFlags(0)
	}

	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		// Restore stderr for the final error message so the user can see it.
		if origStderr != nil {
			os.Stderr = origStderr
		}
		fmt.Fprintln(os.Stderr, err)
		debug.Close()
		os.Exit(1)
	}
}
