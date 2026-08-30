//go:build !unix && !windows

package util

// FileLock is a no-op fallback for platforms without flock or LockFileEx
// (js/wasm, plan9, wasip1). The callers (#1337) degrade to their unlocked
// merge-and-write path, same as when the lock file cannot be opened: the
// merge still reduces lost updates, persistence never hard-fails.
func FileLock(lockPath string) (func(), error) {
	return func() {}, nil
}
