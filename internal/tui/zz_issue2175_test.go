package tui

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

	for i, err := range errs {
		if err == nil || err.Error() != "disabled" {
			t.Fatalf("call %d: want disabled error, got %v", i, err)
		}
	}
}
