package tui

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
)

func TestIssue2277ConcurrentAppendNoRace(t *testing.T) {

	m := &Model{a2aEventState: &a2aEventBufferState{}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.appendA2AEvent(a2a.TaskEventMessage{TaskID: "t"})
		}()
	}
	wg.Wait()
	state := m.ensureA2AEventState()
	state.mu.Lock()
	defer state.mu.Unlock()

	if len(state.events) != 20 {
		t.Errorf("buffer must clamp to its 20-event cap, got %d", len(state.events))
	}
}
