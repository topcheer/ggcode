package tui

// Reproduction meter for the 2026-09-18 startup freeze: a 1862-message
// resumed session showed a 10s event-loop gap with handler meters
// attributing only 291ms - the working hypothesis is full-history View
// rendering. This bench times View() with a large chat history so the
// next fix has a before/after number.

import (
	"strings"
	"testing"
	"time"
)

func TestViewRenderLargeHistoryTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("timing bench")
	}
	m := newTestModel()
	for i := 0; i < 1862; i++ {
		m.chatWriteUser(nextSystemID(), strings.Repeat("line of history text ", 6))
	}
	// Warm once (cache paths), then measure steady-state renders.
	_ = m.View()
	start := time.Now()
	_ = m.View()
	t.Logf("second View() with 1862 messages: %s", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	_ = m.View()
	t.Logf("third  View() with 1862 messages: %s", time.Since(start).Round(time.Millisecond))
}
