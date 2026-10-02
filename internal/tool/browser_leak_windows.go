//go:build windows

package tool

import "os"

// processAlive on Windows always reports false: the pid probe relies on
// POSIX signal 0 semantics. This is safe for the profile GC because
// Windows Chrome does not create the SingletonLock symlink (it uses a
// lockfile instead), so singletonLockPID already returns 0 there and the
// alive-check branch is never taken; GC falls through to the TTL rule
// (#3114: that fallthrough was the bug - see windowsProfileLockHeld and
// the 'system' exemption in gcStaleBrowserProfiles for the fix).
func processAlive(pid int) bool {
	return false
}

// windowsProfileLockHeld reports whether the Chromium user-data-dir holds
// a 'lockfile' - the Windows equivalent of the unix SingletonLock. A live
// Chrome keeps it in place for the lifetime of the browser process; a
// normal exit removes it. Presence therefore means "a Chrome may own this
// dir" and the GC must skip it (#3114): after the TTL passes, a live
// Chrome's lockfile is the only thing standing between the GC sweep and
// deleting a live browser's cookies/sessions/extensions. Crash residue
// makes this conservative in the safe direction (disk leak, not data
// loss), mirroring the unix SingletonLock tradeoff.
func windowsProfileLockHeld(profileDir string) bool {
	_, err := os.Stat(lockfilePath(profileDir))
	return err == nil
}
