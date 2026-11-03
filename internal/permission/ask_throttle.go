package permission

import (
	"encoding/json"
	"sync"
	"time"
)

type AskThrottle struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	denials   map[string][]time.Time
}

const (
	DefaultAskThrottleWindow    = time.Minute
	DefaultAskThrottleThreshold = 2
)

func NewAskThrottle() *AskThrottle {
	return &AskThrottle{
		window:    DefaultAskThrottleWindow,
		threshold: DefaultAskThrottleThreshold,
		denials:   make(map[string][]time.Time, 8),
	}
}

func (t *AskThrottle) key(toolName string, input json.RawMessage) string {
	k, _ := MakeKey(toolName, input)
	return k
}

func (t *AskThrottle) RecordDenial(toolName string, input json.RawMessage) {
	if t == nil {
		return
	}
	k := t.key(toolName, input)
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.denials[k] = append(t.prune(t.denials[k], now), now)
}

func (t *AskThrottle) ShouldSuppress(toolName string, input json.RawMessage) bool {
	if t == nil {
		return false
	}
	k := t.key(toolName, input)
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.prune(t.denials[k], now)) >= t.threshold
}

func (t *AskThrottle) prune(times []time.Time, now time.Time) []time.Time {
	kept := times[:0]
	for _, at := range times {
		if now.Sub(at) <= t.window {
			kept = append(kept, at)
		}
	}
	return kept
}
