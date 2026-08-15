//go:build windows

package daemon

// processCmdline returns "" on Windows: there is no portable in-process way
// to read another process's command line without the Windows API toolchain
// helpers. Callers treat "" as "identity unknown" and keep the signal-0
// liveness verdict, preserving prior behavior (#412).
func processCmdline(pid int) string { return "" }
