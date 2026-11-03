package tui

import "sync"

type streamViewStateData struct {
	mu         sync.RWMutex
	snapshot   string
	writeCount int
	width      int
	height     int
}

func (s *streamViewStateData) setSnapshot(content string) {
	s.mu.Lock()
	s.snapshot = content
	s.writeCount++
	s.mu.Unlock()
}

func (s *streamViewStateData) getSnapshot() (string, int) {
	s.mu.RLock()
	snap := s.snapshot
	count := s.writeCount
	s.mu.RUnlock()
	return snap, count
}

func (s *streamViewStateData) setSize(width, height int) {
	s.mu.Lock()
	s.width = width
	s.height = height
	s.mu.Unlock()
}

func (s *streamViewStateData) getSize() (int, int) {
	s.mu.RLock()
	w, h := s.width, s.height
	s.mu.RUnlock()
	return w, h
}
