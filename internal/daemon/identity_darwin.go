//go:build darwin

package daemon

import (
	"strings"
	"syscall"
)

// processCmdline returns a best-effort command line for pid on macOS via
// the sysctl kern.procargs2 blob.
//
// Layout: int32 argc + argv[0]\0argv[1]\0...\0\0 + FULL ENV REGION.
// #431: the old code matched against the whole blob, so an unrelated
// process whose ENV contains our marker (e.g. a shell that exported
// DAEMON_ARGS="--__daemonized") was misidentified as the daemon after PID
// reuse. We truncate at the argv/env boundary instead.
//
// Returns "" when the command line cannot be inspected — callers treat
// that as "identity unknown" and keep the signal-0 verdict (#412).
func processCmdline(pid int) string {
	if pid <= 0 {
		return ""
	}
	raw, err := syscall.Sysctl("kern.procargs2." + itoaDaemon(pid))
	if err != nil {
		return ""
	}
	if cmdline := trimProcargsToArgv(raw); cmdline != "" {
		return cmdline
	}
	// Fallback for unexpected layouts: full blob (pre-#431 behavior).
	return strings.ReplaceAll(raw, "\x00", " ")
}

// trimProcargsToArgv extracts the argv region from a kern.procargs2 blob,
// excluding the environment. Layout: [4-byte argc][argv strings, NUL-
// separated][NUL padding][env strings]. We walk argc NUL-terminated
// strings, then trim the padding before the env region.
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
