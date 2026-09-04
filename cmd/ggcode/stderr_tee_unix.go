//go:build !windows

package main

import (
	"os"
	"sync"
	"syscall"
)

// teeStderrFD duplicates fd 2 so runtime-level fatal dumps are captured too.
//
// Reassigning the os.Stderr VARIABLE only redirects writes made through Go's
// os package. The Go runtime writes panic and fatal-error goroutine dumps
// (concurrent map access, out of memory, SIGSEGV, ...) DIRECTLY to file
// descriptor 2, bypassing os.Stderr entirely - on Linux those dumps then hit
// the TUI-corrupted terminal and are lost, which is exactly how two crash
// post-mortems lost their headers.
//
// This helper: dups the original fd 2 (so it can be restored), then dups the
// pipe's write end ONTO fd 2. Every byte written to fd 2 - from os.Stderr or
// from the runtime - now lands in the pipe the reader goroutine drains.
// The returned restore function puts the original terminal back on fd 2.
func teeStderrFD(w *os.File) (restore func(), ok bool) {
	origFD, err := syscall.Dup(2)
	if err != nil {
		return nil, false
	}
	if err := syscall.Dup2(int(w.Fd()), 2); err != nil {
		_ = syscall.Close(origFD)
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Dup2(origFD, 2)
			_ = syscall.Close(origFD)
		})
	}, true
}
