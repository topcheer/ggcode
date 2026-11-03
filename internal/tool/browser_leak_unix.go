//go:build !windows

package tool

import "syscall"

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func windowsProfileLockHeld(profileDir string) bool {
	return false
}
