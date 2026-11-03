package tui

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

	_ = m.View()
	start := time.Now()
	_ = m.View()
	t.Logf("second View() with 1862 messages: %s", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	_ = m.View()
	t.Logf("third  View() with 1862 messages: %s", time.Since(start).Round(time.Millisecond))
}
