//go:build windows

package tool

import "os"

func processAlive(pid int) bool {
	return false
}

func windowsProfileLockHeld(profileDir string) bool {
	_, err := os.Stat(lockfilePath(profileDir))
	return err == nil
}
