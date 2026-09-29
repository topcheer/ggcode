//go:build windows

package tmux

import (
	"github.com/topcheer/ggcode/internal/util"
)

// lockStoreFileCrossProc acquires a bounded exclusive lock on the store's
// lock sidecar. #1313: same rationale as the unix side — the prior
// package-level sync.Mutex only serialized in-process savers. The sidecar
// holds no data (learned from the knight/session lock families: never read
// data through a byte that may be locked).
//
// #2809: the blocking LockFileEx (no LOCKFILE_FAIL_IMMEDIATELY) was
// unbounded. Delegated to util.FileLock (FAIL_IMMEDIATELY + bounded retry).
func lockStoreFileCrossProc(path string) (func(), error) {
	return util.FileLock(path + ".flock")
}
