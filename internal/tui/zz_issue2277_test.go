package tui

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
)

// #2277: concurrent a2a task-event callbacks must not race. The old
// appendA2AEvent wrote the bare m.a2aEventBuf mirror field outside the
// lock - two concurrent callbacks were a guaranteed -race report.
func TestIssue2277ConcurrentAppendNoRace(t *testing.T) {
	// #2277: real Models pre-build a2aEventState in NewModel; simulate that
	// (a bare &Model{} would take the racy legacy fallback).
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
	// the buffer keeps only the last 20 events by design
	if len(state.events) != 20 {
		t.Errorf("buffer must clamp to its 20-event cap, got %d", len(state.events))
	}
}
