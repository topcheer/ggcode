//go:build !windows

package tmux

import (
	"os"
	"syscall"
)

// lockStoreFileCrossProc acquires a blocking exclusive flock on the store's
// lock sidecar. #1313: the previous "cross-process lock" was a package-level
// sync.Mutex — pure in-process mutual exclusion, useless when two ggcode
// terminals in different processes save the shared ~/.ggcode/tmux-panes.json
// concurrently (last writer silently dropped the other's workspace panes).
func lockStoreFileCrossProc(path string) (func(), error) {
	f, err := os.OpenFile(path+".flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
