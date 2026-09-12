package tui

// #2175 regression: ensureCurrentWorkspaceIMManager ran check-and-set
// with NO guard from Cmd goroutines (ensurePCReady + 8 panels) while
// the Update loop kept reading m.imManager - duplicate InitRuntime
// races and an unlocked config.IM.Enabled flip + saveConfig.

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestEnsureCurrentWorkspaceIMManagerSerialized(t *testing.T) {
	m := newTestModel()
	m.config = &config.Config{}

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = m.ensureCurrentWorkspaceIMManager("unavailable", "disabled", false)
		}(i)
	}
	wg.Wait()
	// IM disabled + autoEnable=false: every call must return the
	// disabled error - run under -race: the serialized check-and-set
	// must not race the config.IM.Enabled read either.
	for i, err := range errs {
		if err == nil || err.Error() != "disabled" {
			t.Fatalf("call %d: want disabled error, got %v", i, err)
		}
	}
}
