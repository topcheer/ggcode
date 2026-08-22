package tool

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regression: a command whose final output lands inside the 300ms throttle
// window must still emit its tail once - the streaming view must not lose
// the last burst when the process exits mid-window.
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

	// Prime the throttle window so the next Write is suppressed.
	w.Write([]byte("first line\n"))
	w.Write([]byte("last burst line\n")) // inside throttle window: buffered only

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

// Flush must also update the throttle timestamp semantics: a flush followed
// by more output should behave sanely (flush does not wedge the writer).
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
	w.Write([]byte("a\n"))   // emission 1 (window fresh)
	w.flush()                // emission 2 (forced)
	w.lastEmit = time.Time{} // simulate window expiry
	w.Write([]byte("b\n"))   // emission 3 (window expired)
	w.flush()                // no-op: line b already emitted? No - flush is
	// unconditional tail emission, so this is emission 4.
	mu.Lock()
	defer mu.Unlock()
	if len(emissions) != 4 {
		t.Fatalf("expected 4 emissions (write, flush, write, flush), got %d: %v", len(emissions), emissions)
	}
}
