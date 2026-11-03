//go:build !windows

package util

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !isZombieUnix(pid)
}

func isZombieUnix(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return false
	}

	idx := strings.LastIndexByte(string(data), ')')
	if idx < 0 || idx+2 >= len(data) {
		return false
	}
	state := data[idx+2]
	switch state {
	case 'Z', 'X', 'x':
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

func IsProcessAliveProc(proc *os.Process) bool {
	if proc == nil || proc.Pid <= 0 {
		return false
	}
	return IsProcessAlive(proc.Pid)
}
