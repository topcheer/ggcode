//go:build windows

package tmux

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockStoreFileCrossProc acquires a blocking exclusive LockFileEx on the
// store's lock sidecar. #1313: same rationale as the unix side — the prior
// package-level sync.Mutex only serialized in-process savers. Byte-range
// lock on byte 0; the sidecar holds no data (learned from the knight/
// session lock families: never read data through a byte that may be locked).
func lockStoreFileCrossProc(path string) (func(), error) {
	f, err := os.OpenFile(path+".flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// LOCKFILE_EXCLUSIVE_LOCK, blocking (no LOCKFILE_FAIL_IMMEDIATELY).
	if err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{}); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
		f.Close()
	}, nil
}
