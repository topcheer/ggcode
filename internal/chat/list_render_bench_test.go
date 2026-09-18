package chat

// Reproduction for the 2026-09-18 startup freeze: the live capture showed
// `slow view render: 10.008s` on the FIRST frame after resuming a session
// with ~1946 items + a 357K-token checkpoint summary. This bench pins the
// cost at the chat.List level (the earlier TUI bench used only tiny plain
// user items and measured ~0ms, missing the real shape).

import (
	"strings"
	"testing"
	"time"
)

func TestListRenderLargeHistoryFirstFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("timing bench")
	}
	styles := DefaultStyles()
	l := NewList(100, 40)

	// 1945 ordinary assistant turns with realistic bodies.
	for i := 0; i < 1945; i++ {
		a := NewAssistantItem("a", styles)
		a.SetFinished()
		a.SetText("Here is the change summary:\n\n```go\n" +
			strings.Repeat("func example() { /* body */ }\n", 8) +
			"```\n\nNext steps listed below.")
		l.Append(a)
	}
	// The checkpoint summary: 357K tokens ~ 1.4MB of text, appended last.
	sum := NewAssistantItem("summary", styles)
	sum.SetFinished()
	sum.SetText(strings.Repeat("line of checkpoint summary text\n", 48000))
	l.Append(sum)

	l.SetFollow(true) // resume lands at the bottom
	start := time.Now()
	_ = l.Render()
	t.Logf("first Render (1946 items + 1.4MB summary, follow): %s", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	_ = l.Render()
	t.Logf("second Render: %s", time.Since(start).Round(time.Millisecond))
}
