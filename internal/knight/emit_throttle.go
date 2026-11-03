package knight

import (
	"strings"
	"sync"
	"time"
)

type EmitSeverity int

const (
	EmitSeverityInfo EmitSeverity = iota
	EmitSeverityNotice
	EmitSeverityActionRequired
	EmitSeverityError
)

func (s EmitSeverity) String() string {
	switch s {
	case EmitSeverityNotice:
		return "notice"
	case EmitSeverityActionRequired:
		return "action_required"
	case EmitSeverityError:
		return "error"
	default:
		return "info"
	}
}

const (
	defaultEmitThrottleWindow = 6 * time.Hour
)

type emitThrottle struct {
	mu     sync.Mutex
	last   map[string]time.Time
	window time.Duration
}

func newEmitThrottle(window time.Duration) *emitThrottle {
	if window <= 0 {
		window = defaultEmitThrottleWindow
	}
	return &emitThrottle{
		last:   make(map[string]time.Time),
		window: window,
	}
}

func (t *emitThrottle) allow(key string, severity EmitSeverity, now time.Time) bool {
	if t == nil {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	storeKey := key + "|" + severity.String()
	if last, ok := t.last[storeKey]; ok {
		if now.Sub(last) < t.window {
			return false
		}
	}
	t.last[storeKey] = now
	return true
}

func (t *emitThrottle) reset(key string) {
	if t == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.last {
		if strings.HasPrefix(k, key+"|") {
			delete(t.last, k)
		}
	}
}
