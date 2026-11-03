//go:build linux || freebsd || netbsd || openbsd || dragonfly

package daemon

import (
	"os"
	"strings"
)

func processCmdline(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(procCmdlinePath(pid))
	if err != nil || len(data) == 0 {
		return ""
	}

	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return strings.Join(args, " ")
}

func procCmdlinePath(pid int) string {
	return "/proc/" + itoaDaemon(pid) + "/cmdline"
}
