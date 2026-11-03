package tool

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamingProgressWriterFlushEmitsThrottledTail(t *testing.T) {
	var mu sync.Mutex
	var emissions []string
	progress := func(_, _, out string) {
		mu.Lock()
		emissions = append(emissions, out)
		mu.Unlock()
	}

	var buf bytes.Buffer
	w := newStreamingProgressWriter(&buf, nil, progress)

	w.Write([]byte("first line\n"))
	w.Write([]byte("last burst line\n"))

	mu.Lock()
	n := len(emissions)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected exactly 1 throttled emission after two rapid writes, got %d", n)
	}

	w.flush()

	mu.Lock()
	defer mu.Unlock()
	if len(emissions) != 2 {
		t.Fatalf("flush must emit the suppressed tail, got %d emissions", len(emissions))
	}
	last := emissions[len(emissions)-1]
	if !strings.Contains(last, "last burst line") {
		t.Fatalf("flushed emission missing tail content: %q", last)
	}
}

func TestStreamingProgressWriterFlushEmptyNoEmit(t *testing.T) {
	var count int
	progress := func(_, _, _ string) { count++ }
	var buf bytes.Buffer
	w := newStreamingProgressWriter(&buf, nil, progress)
	w.flush()
	if count != 0 {
		t.Fatalf("flush with no lines must not emit, got %d", count)
	}
}

func TestStreamingProgressWriterFlushThenWrite(t *testing.T) {
	var mu sync.Mutex
	var emissions []string
	progress := func(_, _, out string) {
		mu.Lock()
		emissions = append(emissions, out)
		mu.Unlock()
	}
	var buf bytes.Buffer
	w := newStreamingProgressWriter(&buf, nil, progress)
	w.Write([]byte("a\n"))
	w.flush()
	w.lastEmit = time.Time{}
	w.Write([]byte("b\n"))
	w.flush()

	mu.Lock()
	defer mu.Unlock()
	if len(emissions) != 4 {
		t.Fatalf("expected 4 emissions (write, flush, write, flush), got %d: %v", len(emissions), emissions)
	}
}
