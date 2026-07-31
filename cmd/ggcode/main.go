package main

import (
	"fmt"
	"log"
	"os"
	"sync"

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

func main() {
	defer debug.Close()

	// Redirect os.Stderr at the file descriptor level. This catches ALL
	// writes to stderr regardless of how the library obtained the fd:
	// log.New(os.Stderr, ...), fmt.Fprint(os.Stderr, ...), etc.
	redirectStderr()

	// Also redirect the standard log package's default output.
	log.SetOutput(logWriter{})
	log.SetFlags(0)

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
