package tui

import "sync"

// streamViewStateData holds the stream view snapshot behind a pointer so that
// all Bubble Tea Model copies share the same storage. Bubble Tea copies the
// Model value for each View() call, so value-type fields (string, sync.RWMutex)
// would be independent per copy and the stream goroutine would never see updates.
type streamViewStateData struct {
	mu         sync.RWMutex
	snapshot   string
	writeCount int // diagnostic counter
}

// setSnapshot writes the latest View() content (thread-safe).
func (s *streamViewStateData) setSnapshot(content string) {
	s.mu.Lock()
	s.snapshot = content
	s.writeCount++
	s.mu.Unlock()
}

// getSnapshot reads the latest View() content (thread-safe).
func (s *streamViewStateData) getSnapshot() (string, int) {
	s.mu.RLock()
	snap := s.snapshot
	count := s.writeCount
	s.mu.RUnlock()
	return snap, count
}
