package chat

import (
	"strings"
	"testing"
)

// Window-focused variant: the document grows far past the streaming
// window so per-chunk cost must stay flat (proportional to the ~600-line
// window, not the accumulated document).
func BenchmarkStreamUpdateTextLongDoc(b *testing.B) {
	styles := DefaultStyles()
	l := NewList(120, 40)
	a := NewAssistantItem("a0", styles)
	l.Append(a)

	base := strings.Repeat("Long markdown **paragraph** with `code` and lists\n\n- a\n- b\n", 7000)
	a.SetText(base)
	l.Render()
	buf := base
	chunk := "more streamed text "
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf += chunk
		a.SetText(buf)
		l.Render()
	}
}
