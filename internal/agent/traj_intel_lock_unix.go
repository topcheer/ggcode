//go:build !windows

package agent

import (
	"os"
	"syscall"
	"time"
)

// trajLockDeadline bounds how long lockTrajFile waits for the flock before
// giving up (5s, matching the Windows counterpart). #1512 case C: the
// trajectory JSONL is workspace-shared across Agent instances and
// processes; the in-memory s.mu cannot serialize them.
const trajLockDeadline = 5 * time.Second

// lockTrajFile takes an exclusive cross-process flock on lockPath,
// creating it if needed. The returned unlock func must be called
// (defer). #1512 case C: see trajLockDeadline.
//
// #2770: this runs from the post-run defer block, whose contract is
// "Must never panic or block" (traj_intel.go). A plain LOCK_EX blocks
// indefinitely when another instance holds the flock and hangs (a real
// scenario in shared multi-instance workspaces), stalling the whole run
// teardown. Like the Windows counterpart, acquisition is bounded: retry
// LOCK_EX|LOCK_NB until the deadline, then return os.ErrDeadlineExceeded
// so the caller can drop persistence for this run (learnings are
// expendable; teardown is not).
func lockTrajFile(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(trajLockDeadline)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, os.ErrDeadlineExceeded
		}
		time.Sleep(50 * time.Millisecond)
	}
}
