package tool

import (
	"os"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/util"
)

// preWriteHook is called before a file write to save a checkpoint of the
// old content. Set via SetPreWriteHook during agent initialization.
var preWriteHook func(filePath, oldContent, newContent, toolCall string)

// SetPreWriteHook sets a global hook called before file writes.
// The hook receives (filePath, oldContent, newContent, toolName) and is
// used to save undo checkpoints.
func SetPreWriteHook(fn func(filePath, oldContent, newContent, toolCall string)) {
	preWriteHook = fn
}

// CheckpointSaver returns a pre-write hook that saves checkpoints to the
// given manager. This is the standard hook used by TUI/daemon/ACP modes.
func CheckpointSaver(mgr *checkpoint.Manager) func(filePath, oldContent, newContent, toolCall string) {
	return func(filePath, oldContent, newContent, toolCall string) {
		mgr.Save(filePath, oldContent, newContent, toolCall)
	}
}

// atomicWriteFile writes data to a file atomically. Before writing, if a
// pre-write hook is set and the file already exists, the old content is
// captured and passed to the hook for checkpoint/undo support.
func atomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	if preWriteHook != nil {
		if oldData, err := os.ReadFile(path); err == nil {
			preWriteHook(path, string(oldData), string(data), "")
		}
	}
	return util.AtomicWriteFile(path, data, defaultMode)
}
