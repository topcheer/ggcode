package tui

import (
	"sync"
	"time"
)

// textThrottleMap rate-limits high-frequency per-id stream events (e.g.
// teammate_text) so the TUI does not re-render on every chunk. It was
// previously defined alongside the follow-panel code but is used by the
// swarm event wiring in repl.go.
type textThrottleMap struct {
	mu    sync.Mutex
	last  map[string]time.Time
	delay time.Duration
}

func newTextThrottleMap(delay time.Duration) *textThrottleMap {
	return &textThrottleMap{last: make(map[string]time.Time), delay: delay}
}

func (t *textThrottleMap) Allow(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	last := t.last[id]
	if !last.IsZero() && now.Sub(last) < t.delay {
		return false
	}
	t.last[id] = now
	return true
}

func (t *textThrottleMap) Clear(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, id)
}
