package tool

import (
	"fmt"
	"os"
	"sync"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/util"
)

type PreWriteHook func(filePath, oldContent, newContent, toolCall string) error

var (
	preWriteMu     sync.Mutex
	preWriteHook   PreWriteHook
	preWriteExtras []*preWriteEntry
)

type preWriteEntry struct{ fn PreWriteHook }

func SetPreWriteHook(fn PreWriteHook) {
	preWriteMu.Lock()
	preWriteHook = fn
	preWriteMu.Unlock()
}

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

func CheckpointSaver(mgr *checkpoint.Manager) func(filePath, oldContent, newContent, toolCall string) error {
	return func(filePath, oldContent, newContent, toolCall string) error {
		mgr.Save(filePath, oldContent, newContent, toolCall)
		return nil
	}
}

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
