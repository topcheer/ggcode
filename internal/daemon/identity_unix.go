//go:build linux || freebsd || netbsd || openbsd || dragonfly

package daemon

import (
	"os"
	"strings"
)

// processCmdline returns a best-effort command line for pid on
// procfs-bearing Unixes by reading /proc/<pid>/cmdline (NUL-separated args
// joined by spaces). /proc/pid/cmdline contains argv only — no environment
// region — so no argv/env truncation is needed here (that is the macOS
// concern handled in identity_darwin.go, #431).
//
// Returns "" when the command line cannot be inspected — callers treat
// that as "identity unknown" and keep the signal-0 verdict (#412).
func processCmdline(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(procCmdlinePath(pid))
	if err != nil || len(data) == 0 {
		return ""
	}
	// Args are NUL-separated; the trailing NUL terminates the last arg.
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return strings.Join(args, " ")
}

func procCmdlinePath(pid int) string {
	return "/proc/" + itoaDaemon(pid) + "/cmdline"
}
