//go:build windows

package main

import "os"

// teeStderrFD is a no-op on Windows: syscall.Dup2 is not available for CRT
// descriptors, so stderr capture stays at the os.Stderr-variable level.
// Runtime fatal dumps are therefore not captured on Windows; the recoverable
// panic path (WriteCrashLog) still works.
func teeStderrFD(w *os.File) (restore func(), ok bool) {
	return nil, false
}
