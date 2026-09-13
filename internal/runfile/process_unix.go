//go:build unix

package runfile

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processExists checks if a process with the given PID is running.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	// #799: EPERM means the process EXISTS but belongs to another user
	// (shared-HOME/sudo layouts); treating it as dead made callers delete
	// live instances' port files cross-user.
	return err == nil || err == syscall.EPERM
}

// procStartTime returns the kernel start time (clock ticks since boot,
// /proc/<pid>/stat field 22) as the process's identity token, or "" when
// /proc is unavailable (macOS: no /proc - the identity check degrades to
// signal-0 liveness). #1624 case C: a recycled PID reported "alive" and
// the ghost port file was never cleaned; comparing start times detects
// the swap.
func procStartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The comm field can contain spaces/parens - split AFTER the last ')'.
	i := bytes.LastIndexByte(data, ')')
	if i < 0 || i+2 > len(data) {
		return ""
	}
	fields := strings.Fields(string(data[i+2:]))
	// fields[0] is state; starttime is field 22 overall, i.e. index 21
	// counting from state=field3 -> 21-2 = index 19 in this slice.
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
