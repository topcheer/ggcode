//go:build darwin

package daemon

import (
	"strings"
	"syscall"
)

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

	return strings.ReplaceAll(raw, "\x00", " ")
}

func trimProcargsToArgv(raw string) string {
	if len(raw) < 4 {
		return ""
	}
	argc := int(uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24)
	if argc <= 0 || argc > 4096 {
		return ""
	}
	rest := raw[4:]

	for i := 0; i < argc; i++ {
		idx := strings.IndexByte(rest, 0)
		if idx < 0 {
			return ""
		}
		rest = rest[idx+1:]
	}

	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	return strings.TrimSpace(strings.ReplaceAll(raw[4:len(raw)-len(rest)], "\x00", " "))
}
