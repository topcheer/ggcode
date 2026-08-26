package tool

import (
	"fmt"
	"os"
	"sync"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/util"
)

// PreWriteHook is called before a file write to save a checkpoint of the
// old content. The hook may return an error to abort the write.
type PreWriteHook func(filePath, oldContent, newContent, toolCall string) error

// Pre-write hook registry. SetPreWriteHook installs the process-wide slot
// used by single-agent CLI paths (TUI/pipe/daemon). AddPreWriteHook (#1047)
// registers an additional scoped hook with a remove func, so multiple
// concurrent ACP sessions each keep their own checkpoint manager instead of
// the package global being overwritten last-writer-wins (which redirected
// every session's checkpoints to the most recently initialized loop).
var (
	preWriteMu     sync.Mutex
	preWriteHook   PreWriteHook
	preWriteExtras []*preWriteEntry
)

type preWriteEntry struct{ fn PreWriteHook }

// SetPreWriteHook sets the process-wide hook called before file writes.
// The hook receives (filePath, oldContent, newContent, toolName) and is
// used to save undo checkpoints. If the hook returns an error, the write is aborted.
func SetPreWriteHook(fn PreWriteHook) {
	preWriteMu.Lock()
	preWriteHook = fn
	preWriteMu.Unlock()
}

// AddPreWriteHook registers an additional pre-write hook and returns a
// remove func. All registered hooks run before each write; the first
// non-nil error aborts the write (#1047).
func AddPreWriteHook(fn PreWriteHook) (remove func()) {
	e := &preWriteEntry{fn: fn}
	preWriteMu.Lock()
	preWriteExtras = append(preWriteExtras, e)
	preWriteMu.Unlock()
	return func() {
		preWriteMu.Lock()
		for i, x := range preWriteExtras {
			if x == e {
				preWriteExtras = append(preWriteExtras[:i], preWriteExtras[i+1:]...)
				break
			}
		}
		preWriteMu.Unlock()
	}
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

// atomicWriteFile writes data to a file atomically. Before writing, if any
// pre-write hook is registered and the file already exists, the old content
// is captured and passed to the hooks for checkpoint/undo support.
// If a hook returns an error, the write is aborted.
func atomicWriteFile(path string, data []byte, defaultMode os.FileMode) error {
	preWriteMu.Lock()
	hooks := make([]PreWriteHook, 0, 1+len(preWriteExtras))
	if preWriteHook != nil {
		hooks = append(hooks, preWriteHook)
	}
	for _, e := range preWriteExtras {
		hooks = append(hooks, e.fn)
	}
	preWriteMu.Unlock()
	if len(hooks) > 0 {
		if oldData, err := os.ReadFile(path); err == nil {
			for _, h := range hooks {
				if err := h(path, string(oldData), string(data), ""); err != nil {
					return fmt.Errorf("pre-write hook aborted write to %s: %w", path, err)
				}
			}
		}
	}
	return util.AtomicWriteFile(path, data, defaultMode)
}
