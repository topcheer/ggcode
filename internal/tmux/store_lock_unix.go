//go:build !windows

package tmux

import (
	"github.com/topcheer/ggcode/internal/util"
)

// lockStoreFileCrossProc acquires a bounded exclusive flock on the store's
// lock sidecar. #1313: the previous "cross-process lock" was a package-level
// sync.Mutex — pure in-process mutual exclusion, useless when two ggcode
// terminals in different processes save the shared ~/.ggcode/tmux-panes.json
// concurrently (last writer silently dropped the other's workspace panes).
//
// #2809: the bare blocking LOCK_EX was unbounded - a suspended holder hung
// the saver forever. Delegated to util.FileLock (#1834 case 2 pattern:
// LOCK_NB + bounded retry, error on timeout; saveWorkspaceState degrades
// via its existing error path).
func lockStoreFileCrossProc(path string) (func(), error) {
	return util.FileLock(path + ".flock")
}
