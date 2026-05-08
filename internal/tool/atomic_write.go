package tool

import (
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/util"
)

// preWriteHook is called before a file write to save a checkpoint of the
// old content. Set via SetPreWriteHook during agent initialization.
// The hook may return an error to abort the write.
var preWriteHook func(filePath, oldContent, newContent, toolCall string) error

// SetPreWriteHook sets a global hook called before file writes.
// The hook receives (filePath, oldContent, newContent, toolName) and is
// used to save undo checkpoints. If the hook returns an error, the write is aborted.
func SetPreWriteHook(fn func(filePath, oldContent, newContent, toolCall string) error) {
	preWriteHook = fn
}

// CheckpointSaver returns a pre-write hook that saves checkpoints to the
// given manager. This is the standard hook used by TUI/daemon/ACP modes.
// Checkpoint saving is in-memory only; errors are logged but do not abort writes.
func CheckpointSaver(mgr *checkpoint.Manager) func(filePath, oldContent, newContent, toolCall string) error {
	return func(filePath, oldContent, newContent, toolCall string) error {
		mgr.Save(filePath, oldContent, newContent, toolCall)
		return nil
	}
}

// atomicWriteFile writes data to a file atomically. Before writing, if a
// pre-write hook is set and the file already exists, the old content is
// captured and passed to the hook for checkpoint/undo support.
// If the hook returns an error, the write is aborted.
func atomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	if preWriteHook != nil {
		if oldData, err := os.ReadFile(path); err == nil {
			if err := preWriteHook(path, string(oldData), string(data), ""); err != nil {
				return fmt.Errorf("pre-write hook aborted write to %s: %w", path, err)
			}
		}
	}
	return util.AtomicWriteFile(path, data, defaultMode)
}
