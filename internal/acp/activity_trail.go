package acp

import (
	"strings"
	"sync"
)

const (
	activityTrailLimit     = 24
	activityTrailLineLimit = 200
)

type activityTrail struct {
	mu      sync.Mutex
	entries []string
}

func (t *activityTrail) Add(msg string) {
	msg = compactActivityTrailLine(msg)
	if msg == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, msg)
	if len(t.entries) > activityTrailLimit {
		t.entries = append([]string(nil), t.entries[len(t.entries)-activityTrailLimit:]...)
	}
}

func (t *activityTrail) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = t.entries[:0]
}

func (t *activityTrail) Snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.entries, "\n")
}

func compactActivityTrailLine(msg string) string {
	msg = strings.Join(strings.Fields(strings.TrimSpace(msg)), " ")
	if msg == "" {
		return ""
	}
	runes := []rune(msg)
	if len(runes) <= activityTrailLineLimit {
		return msg
	}
	return string(runes[:activityTrailLineLimit-3]) + "..."
}
