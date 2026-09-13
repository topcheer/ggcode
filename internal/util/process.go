//go:build !windows

package util

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// IsProcessAlive checks if a process with the given PID is still running.
// On Unix, it sends signal 0 (no signal, just permission/existence check).
//
// #1839: a zombie (exited but not yet reaped by its parent) also answers
// signal 0, so the raw check would report a dead process as alive. Callers
// that only decide whether to KEEP state (crash-recovery anchors, instance
// files) fail safe on residue; but semantics here should be honest - a
// zombie is not running. When /proc is available we read the process state
// and treat Z (and X/x, exiting) as dead. /proc-less platforms keep the
// signal-0-only behavior (documented, conservative).
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// #2190: kill(pid, 0) EPERM means the process EXISTS but we lack
	// permission to signal it (POSIX) - treating it as dead mirrors the
	// exact bug Windows fixed in #1723 (a privileged daemon probed by an
	// unprivileged caller: false-dead deletes the PID file and forks a
	// second instance, or triggers concurrent crash recovery - the Unix
	// twin of #1490). Only a real error (ESRCH et al.) means dead.
	if err := proc.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !isZombieUnix(pid)
}

// isZombieUnix reports whether /proc/<pid>/stat shows the process in an
// exited (zombie or dying) state. Returns false on any read/parse failure -
// liveness stays with the signal-0 verdict when /proc is unavailable.
func isZombieUnix(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// Layout: pid (comm) state ... - the comm field may contain spaces or
	// parens, so parse from the LAST ')'.
	idx := strings.LastIndexByte(string(data), ')')
	if idx < 0 || idx+2 >= len(data) {
		return false
	}
	state := data[idx+2]
	switch state {
	case 'Z', 'X', 'x': // zombie / dead / dying
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// IsProcessAliveProc checks if the given os.Process is still running.
func IsProcessAliveProc(proc *os.Process) bool {
	if proc == nil || proc.Pid <= 0 {
		return false
	}
	return IsProcessAlive(proc.Pid)
}
