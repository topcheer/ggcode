//go:build unix

package daemon

import (
	"os"
	"strings"
	"syscall"
)

// processCmdline returns a best-effort command line for pid. On Linux it
// reads /proc/<pid>/cmdline (NUL-separated args joined by spaces). On macOS
// and other Unixes without procfs it uses sysctl KERN_PROCARGS2. It returns
// "" when the command line cannot be inspected — callers treat that as
// "identity unknown" and keep the signal-0 verdict (#412).
func processCmdline(pid int) string {
	if pid <= 0 {
		return ""
	}
	// Linux (and BSDs exposing procfs): /proc/<pid>/cmdline
	if data, err := os.ReadFile(procCmdlinePath(pid)); err == nil {
		if len(data) == 0 {
			return ""
		}
		// Args are NUL-separated; the trailing NUL terminates the last arg.
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		return strings.Join(args, " ")
	}
	// macOS: sysctl kern.procargs2 returns NUL-separated
	// argv[0]\0env...\0argv[1]... — plain substring matching against the
	// whole blob is sufficient for identity checks.
	if raw, err := syscall.Sysctl("kern.procargs2." + itoaDaemon(pid)); err == nil {
		return strings.ReplaceAll(raw, "\x00", " ")
	}
	return ""
}

func procCmdlinePath(pid int) string {
	return "/proc/" + itoaDaemon(pid) + "/cmdline"
}

func itoaDaemon(pid int) string {
	if pid == 0 {
		return "0"
	}
	neg := pid < 0
	if neg {
		pid = -pid
	}
	var buf [20]byte
	i := len(buf)
	for pid > 0 {
		i--
		buf[i] = byte('0' + pid%10)
		pid /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
