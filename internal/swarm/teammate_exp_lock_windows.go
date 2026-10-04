//go:build windows

package swarm

import (
	"os"
	"time"
)

// teammateExpLockDeadline bounds how long appendTeammateExperience waits for
// the cross-process lock before giving up (the ledger is best-effort).
const teammateExpLockDeadline = 3 * time.Second

// lockTeammateExpFile is the Windows counterpart of the unix flock version
// (#3260, mirroring internal/agent's traj_intel_lock_windows.go exactly):
// an exclusive-create lock file with bounded retry. The O_EXCL marker is
// sufficient for the lost-update window here and self-heals on process
// exit via the stale-age sweep.
func lockTeammateExpFile(lockPath string) (func(), error) {
	deadline := time.Now().Add(teammateExpLockDeadline)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return func() { f.Close(); os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Stale sweep: a lock older than 30s is from a crashed holder.
		if fi, statErr := os.Stat(lockPath); statErr == nil && time.Since(fi.ModTime()) > 30*time.Second {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, os.ErrDeadlineExceeded
		}
		time.Sleep(50 * time.Millisecond)
	}
}
