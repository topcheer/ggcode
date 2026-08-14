package chat

import (
	"fmt"
	"strings"
	"testing"
)

// benchList builds a list resembling a long-running session: mostly tool
// items with sizable bodies plus assistant messages.
func benchList(b *testing.B, n int) *List {
	styles := DefaultStyles()
	l := NewList(120, 40)
	jsonBody := `{"path":"/some/deeply/nested/project/dir/file.go","result":"` + strings.Repeat("x", 400) + `"}`
	for i := 0; i < n; i++ {
		switch i % 4 {
		case 0:
			item := NewBashToolItem(fmt.Sprintf("t%d", i), "go build ./...", "$ go build ./...\n"+strings.Repeat("ok   package\n", 40), StatusSuccess, styles)
			l.Append(item)
		case 1:
			a := NewAssistantItem(fmt.Sprintf("a%d", i), styles)
			a.SetText(strings.Repeat("Some markdown **text** with `code` spans\n\n- item one\n- item two\n", 15))
			l.Append(a)
		case 2:
			l.Append(NewBaseToolItem(fmt.Sprintf("j%d", i), "grep", StatusSuccess, jsonBody, styles))
		default:
			l.Append(NewSystemItem(fmt.Sprintf("s%d", i), strings.Repeat("system notice line\n", 6), styles))
		}
	}
	l.ScrollToEnd()
	return l
}

// BenchmarkScrollCold measures ScrollDown over never-rendered items —
// the "user scrolls through history for the first time" path.
func BenchmarkScrollCold(b *testing.B) {
	l := benchList(b, 2000)
	l.ScrollUp(20000) // jump to top; most items below never rendered
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.ScrollDown(3) // wheel tick
	}
}

// BenchmarkRenderViewport measures per-frame render cost mid-scroll.
func BenchmarkRenderViewport(b *testing.B) {
	l := benchList(b, 2000)
	l.ScrollUp(5000) // mid-history
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.ScrollUp(1)
		l.Render()
	}
}

// BenchmarkScrollWarm measures scroll over already-rendered items.
func BenchmarkScrollWarm(b *testing.B) {
	l := benchList(b, 2000)
	l.Render()
	for i := 0; i < 400; i++ {
		l.ScrollDown(40)
		l.Render()
		l.ScrollUp(40)
		l.Render()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.ScrollDown(3)
	}
}

// BenchmarkStreamUpdateText measures the per-chunk cost of updating a long
// streaming assistant item (the hot path while the agent is busy): SetText
// invalidates the cache, so each chunk re-renders the FULL accumulated text.
func BenchmarkStreamUpdateText(b *testing.B) {
	styles := DefaultStyles()
	l := NewList(120, 40)
	a := NewAssistantItem("a0", styles)
	l.Append(a)
	// Simulate an item that has accumulated ~30KB of streamed markdown
	// (a long agent reply after many minutes of streaming).
	a.SetText(strings.Repeat("Long markdown **paragraph** with `code` and lists\n\n- a\n- b\n", 700))
	l.Render() // warm
	chunk := "more streamed text "
	buf := strings.Repeat("Long markdown **paragraph** with `code` and lists\n\n- a\n- b\n", 700)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf += chunk
		a.SetText(buf)
		l.Render()
	}
}
