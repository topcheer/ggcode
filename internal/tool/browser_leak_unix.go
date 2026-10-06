//go:build !windows

package tool

import "syscall"

// processAlive reports whether pid currently exists (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// windowsProfileLockHeld is Windows-only (#3114): unix liveness is fully
// covered by the SingletonLock pid + processAlive probe, and unix Chromium
// holds no 'lockfile'. Constant false keeps gcStaleBrowserProfiles
// cross-platform without build-tagged call sites.
func windowsProfileLockHeld(profileDir string) bool {
	return false
}
