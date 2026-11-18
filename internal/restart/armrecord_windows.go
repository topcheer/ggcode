//go:build windows

package restart

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// processAlive reports whether pid currently exists (tasklist probe; the
// unix signal-0 trick has no direct Windows equivalent).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		// Probe unavailable: treat as alive so a live foreign seat is never
		// falsely reported as dead-and-alarming.
		return true
	}
	return strings.Contains(string(out), ","+strconv.Itoa(pid)+",")
}
