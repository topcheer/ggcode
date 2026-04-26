//go:build !unix

package main

import (
	"os"
)

func selfSignal(_ interface{}) {
	// On non-Unix (Windows), just exit the process.
	// The caller sets daemonRestartRequested = true before calling this,
	// and the deferred os.Exit in the caller will handle the actual restart.
	os.Exit(0)
}
