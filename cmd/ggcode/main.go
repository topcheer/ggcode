package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
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

// restoreStderrFD, when non-nil, puts the original terminal file descriptor
// back onto fd 2. It is set by redirectStderr when the fd-level tee was
// installed (see teeStderrFD) and MUST be called before writing anything to
// stderr on shutdown paths - os.Stderr/origStderr still refer to fd 2, which
// the tee repointed at the capture pipe.
var restoreStderrFD func()

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

	// FD-level tee: runtime fatal dumps (concurrent map access, OOM, ...)
	// write directly to fd 2, bypassing the os.Stderr variable swap above.
	// Dup the pipe onto fd 2 so the reader below sees those bytes too, and
	// keep a restore hook for the shutdown paths. Best effort: without it
	// (Windows, dup failure) behavior is unchanged.
	if restore, ok := teeStderrFD(w); ok {
		restoreStderrFD = restore
	}

	var mu sync.Mutex
	buf := make([]byte, 0, 4096)
	// ring keeps the trailing stderr bytes so a crash dump's beginning
	// (written before the reader noticed) is still persisted.
	const ringCap = 256 << 10
	ring := make([]byte, 0, ringCap+4096)
	var dumpFile *os.File

	// startFatalCapture opens the crash capture file on first sight of a
	// runtime-level crash banner, writing everything seen so far.
	startFatalCapture := func() {
		if dumpFile != nil {
			return
		}
		dir := config.ConfigDir() + string(os.PathSeparator) + "crash"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		path := filepath.Join(dir, fmt.Sprintf("fatal-%s.log", time.Now().Format("20060102-150405")))
		f, ferr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if ferr != nil {
			return
		}
		dumpFile = f
		_, _ = f.Write(ring)
	}

	go func() {
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				mu.Lock()
				chunk := tmp[:n]
				// Ring first (raw), so fatal capture includes pre-banner bytes.
				ring = append(ring, chunk...)
				if len(ring) > ringCap {
					ring = ring[len(ring)-ringCap:]
				}
				if dumpFile != nil {
					// Mid-dump: stream raw bytes to the crash file; skip line
					// processing - dumps can be megabytes.
					_, _ = dumpFile.Write(chunk)
					mu.Unlock()
					if err != nil {
						_ = dumpFile.Close()
					}
					if err != nil {
						break
					}
					continue
				}
				buf = append(buf, chunk...)
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
						if looksFatal(line) {
							// Banner sighted: persist ring + remainder, then
							// keep streaming raw.
							buf = append([]byte(line+"\n"), buf...)
							startFatalCapture()
							if dumpFile != nil {
								_, _ = dumpFile.Write([]byte(line + "\n"))
							}
							continue
						}
						debug.Log("stderr", "%s", line)
					}
				}
				mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		if dumpFile != nil {
			mu.Lock()
			rest := buf
			buf = nil
			mu.Unlock()
			if len(rest) > 0 {
				_, _ = dumpFile.Write(rest)
			}
			_ = dumpFile.Close()
		}
	}()
}

// looksFatal reports whether an stderr line is a runtime-level crash banner:
// the moment such a line is seen, the capture reader starts persisting the
// whole stream to ~/.ggcode/crash/fatal-*.log. The goroutine heuristics
// ([running]) catch dump bodies even when the banner line itself was split
// across pipe reads.
func looksFatal(line string) bool {
	return strings.HasPrefix(line, "fatal error:") ||
		strings.HasPrefix(line, "panic:") ||
		strings.HasPrefix(line, "SIGSEGV") ||
		strings.HasPrefix(line, "SIGABRT") ||
		strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, " [running]:")
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
			// Restore fd 2 FIRST: with the fd-level tee active, origStderr (fd 2)
			// points at the capture pipe - the crash banner would vanish into
			// the ring instead of reaching the user's terminal.
			if restoreStderrFD != nil {
				restoreStderrFD()
			}
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
		if restoreStderrFD != nil {
			restoreStderrFD()
		}
		if origStderr != nil {
			os.Stderr = origStderr
		}
		fmt.Fprintln(os.Stderr, err)
		debug.Close()
		os.Exit(1)
	}
}
