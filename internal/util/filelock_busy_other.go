//go:build !unix && !windows

package util

// isLockBusy is the no-op fallback for platforms without file locks
// (js/wasm, plan9, wasip1) - see filelock_other.go. acquireWithRetry is
// compiled on every platform, so the symbol must exist everywhere.
func isLockBusy(err error) bool {
	return false
}
