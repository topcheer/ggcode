package knight

import (
	"strings"
	"sync"
	"time"
)

// EmitSeverity tags an outbound Knight report so downstream consumers (IM,
// EventSink, future filters) can decide what to do with it.
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

// emitThrottle remembers the last time a given (key, severity) message was
// emitted so we don't spam IM/TUI with repeated staging/stale notifications.
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

// allow returns true if it's been longer than `window` since the same
// (key, severity) was last allowed. An empty key always allows.
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

// reset clears the throttle for a key (any severity). Used on successful
// promote/reject so the next staging cycle for the same skill is allowed
// to notify again.
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
