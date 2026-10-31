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
	width      int // terminal size mirrored for the stream goroutine (#4209)
	height     int
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

// setSize mirrors the terminal dimensions into the shared state so the
// stream frameLoop goroutine can read them without racing the Bubble Tea
// model's plain width/height fields (#4209). handleResize writes; the
// viewFunc closures read.
func (s *streamViewStateData) setSize(width, height int) {
	s.mu.Lock()
	s.width = width
	s.height = height
	s.mu.Unlock()
}

// getSize reads the mirrored terminal size (thread-safe).
func (s *streamViewStateData) getSize() (int, int) {
	s.mu.RLock()
	w, h := s.width, s.height
	s.mu.RUnlock()
	return w, h
}
