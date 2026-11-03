//go:build windows

package tmux

import (
	"github.com/topcheer/ggcode/internal/util"
)

func lockStoreFileCrossProc(path string) (func(), error) {
	return util.FileLock(path + ".flock")
}
