//go:build windows

package im

import (
	"github.com/topcheer/ggcode/internal/util"
)

func lockBindingsFile(bindingsPath string) (func(), error) {
	return util.FileLock(bindingsPath + ".flock")
}
