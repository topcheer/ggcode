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
	// macOS: sysctl kern.procargs2 layout is
	// int32 argc + argv[0]\0argv[1]\0...\0\0 + FULL ENV REGION.
	// #431: the old code matched against the whole blob, so an unrelated
	// process whose ENV contains our marker (e.g. a shell that exported
	// DAEMON_ARGS="--__daemonized") was misidentified as the daemon after
	// PID reuse. Truncate at the argv/env boundary: argv is argc NUL-
	// terminated strings followed by a DOUBLE NUL before the env region.
	if raw, err := syscall.Sysctl("kern.procargs2." + itoaDaemon(pid)); err == nil {
		if cmdline := trimProcargsToArgv(raw); cmdline != "" {
			return cmdline
		}
		// Fallback for unexpected layouts: full blob (pre-#431 behavior).
		return strings.ReplaceAll(raw, "\x00", " ")
	}
	return ""
}

// trimProcargsToArgv extracts the argv region from a kern.procargs2 blob,
// excluding the environment. Layout: [4-byte argc][argv strings, NUL-
// separated][NUL padding][env strings]. We scan for the end of argv as the
// first double-NUL after the argc prefix, or stop after the argc'th string.
func trimProcargsToArgv(raw string) string {
	if len(raw) < 4 {
		return ""
	}
	argc := int(uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24)
	if argc <= 0 || argc > 4096 {
		return ""
	}
	rest := raw[4:]
	// Walk argc NUL-terminated strings.
	for i := 0; i < argc; i++ {
		idx := strings.IndexByte(rest, 0)
		if idx < 0 {
			return "" // malformed
		}
		rest = rest[idx+1:]
	}
	// Trim leading padding NULs before the env region.
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	return strings.TrimSpace(strings.ReplaceAll(raw[4:len(raw)-len(rest)], "\x00", " "))
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
