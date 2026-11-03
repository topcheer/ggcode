//go:build windows

package tool

// spawnPressure returns the #4852 Ask-#1 saturation metrics without
// spawning any process. Windows reports fork/exec /bin/sh ENOENT only in
// POSIX-ish environments; there we fall back to the loadavg/rlimit capture
// implemented in the POSIX variants. On native Windows there is no shell
// spawn of this shape, so the capture is a no-op.
func spawnPressure() string {
	return ""
}
