//go:build windows

package tool

// processAlive on Windows always reports false: the pid probe relies on
// POSIX signal 0 semantics. This is safe for the profile GC because
// Windows Chrome does not create the SingletonLock symlink (it uses a
// lockfile instead), so singletonLockPID already returns 0 there and the
// alive-check branch is never taken; GC falls through to the TTL rule.
func processAlive(pid int) bool {
	return false
}
