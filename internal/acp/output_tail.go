package acp

import (
	"strings"
	"sync"
)

const outputTailLimit = 8 * 1024

type outputTail struct {
	mu   sync.Mutex
	data []byte
}

func (t *outputTail) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if len(t.data) > outputTailLimit {
		t.data = append([]byte(nil), t.data[len(t.data)-outputTailLimit:]...)
	}
	return len(p), nil
}

func (t *outputTail) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = t.data[:0]
}

func (t *outputTail) Snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.data))
}
