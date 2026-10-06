//go:build windows

package im

import (
	"github.com/topcheer/ggcode/internal/util"
)

// lockBindingsFile acquires a bounded exclusive lock on the bindings lock
// file (serialization across ggcode processes sharing im-bindings.json).
//
// #2809: the previous hand-rolled kernel32 acquisition was blocking (no
// FAIL_IMMEDIATELY) - a suspended holder wedged s.mu and the whole IM read
// path. Delegated to util.FileLock (bounded retry; callers degrade via their
// error return path).
func lockBindingsFile(bindingsPath string) (func(), error) {
	return util.FileLock(bindingsPath + ".flock")
}
