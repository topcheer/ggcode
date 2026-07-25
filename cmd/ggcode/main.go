package main

import (
	"fmt"
	"log"
	"os"

	"github.com/topcheer/ggcode/internal/debug"
)

// logWriter redirects standard library log output to debug.Log so that
// third-party libraries (pion/turn) writing via the standard log package
// don't corrupt the TUI by writing directly to stderr.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	debug.Log("stderr", "%s", string(p))
	return len(p), nil
}

func main() {
	defer debug.Close()

	// Redirect standard log output away from stderr.
	log.SetOutput(logWriter{})
	log.SetFlags(0)

	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		debug.Close()
		os.Exit(1)
	}
}
