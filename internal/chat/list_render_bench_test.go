package chat

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

	for i := 0; i < 1945; i++ {
		a := NewAssistantItem("a", styles)
		a.SetFinished()
		a.SetText("Here is the change summary:\n\n```go\n" +
			strings.Repeat("func example() { /* body */ }\n", 8) +
			"```\n\nNext steps listed below.")
		l.Append(a)
	}

	sum := NewAssistantItem("summary", styles)
	sum.SetFinished()
	sum.SetText(strings.Repeat("line of checkpoint summary text\n", 48000))
	l.Append(sum)

	l.SetFollow(true)
	start := time.Now()
	_ = l.Render()
	t.Logf("first Render (1946 items + 1.4MB summary, follow): %s", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	_ = l.Render()
	t.Logf("second Render: %s", time.Since(start).Round(time.Millisecond))
}
