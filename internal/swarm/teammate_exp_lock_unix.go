//go:build !windows

package swarm

import (
	"os"
	"syscall"
	"time"
)

// teammateExpLockDeadline bounds how long appendTeammateExperience waits for
// the cross-process lock before giving up (the ledger is best-effort).
const teammateExpLockDeadline = 3 * time.Second

// lockTeammateExpFile mirrors internal/agent's lockTrajFile (#1512-C): an
// exclusive advisory flock on a sidecar .lock file, held across the whole
// read→rewrite→rename window. See #3260: two ggcode processes sharing one
// workspace previously both loaded the same ledger baseline, appended their
// own entry, and the LAST rename won - silently erasing the other's
// teammate experience. agent→swarm import direction forbids reuse, so the
// ~30-line discipline is duplicated per package precedent.
func lockTeammateExpFile(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(teammateExpLockDeadline)
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
