package chat

import (
	"fmt"
	"strings"
	"testing"
)

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

func BenchmarkScrollCold(b *testing.B) {
	l := benchList(b, 2000)
	l.ScrollUp(20000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.ScrollDown(3)
	}
}

func BenchmarkRenderViewport(b *testing.B) {
	l := benchList(b, 2000)
	l.ScrollUp(5000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.ScrollUp(1)
		l.Render()
	}
}

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

func BenchmarkStreamUpdateText(b *testing.B) {
	styles := DefaultStyles()
	l := NewList(120, 40)
	a := NewAssistantItem("a0", styles)
	l.Append(a)

	a.SetText(strings.Repeat("Long markdown **paragraph** with `code` and lists\n\n- a\n- b\n", 700))
	l.Render()
	chunk := "more streamed text "
	buf := strings.Repeat("Long markdown **paragraph** with `code` and lists\n\n- a\n- b\n", 700)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf += chunk
		a.SetText(buf)
		l.Render()
	}
}
