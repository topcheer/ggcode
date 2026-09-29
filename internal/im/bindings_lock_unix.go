//go:build !windows

package im

import (
	"github.com/topcheer/ggcode/internal/util"
)

// lockBindingsFile acquires a bounded exclusive flock on the bindings lock
// file. This serializes read-modify-write cycles across multiple ggcode
// processes that share the same im-bindings.json file. The returned cleanup
// function releases the lock and closes the file descriptor.
//
// #2809: the four writers call this while already holding s.mu - a bare
// blocking LOCK_EX meant a live-but-suspended holder (SIGSTOP, App Nap,
// giant GC) cascaded into s.mu being held forever and every List* call
// wedging: the IM adapter silently stopped responding. Now delegated to
// util.FileLock (#1834 case 2 pattern: LOCK_NB + bounded retry, error out
// on timeout; every caller already degrades via its error return path).
func lockBindingsFile(bindingsPath string) (func(), error) {
	return util.FileLock(bindingsPath + ".flock")
}
