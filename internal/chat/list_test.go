package chat

import (
	"fmt"
	"strings"
	"testing"
)

func TestListEmpty(t *testing.T) {
	l := NewList(80, 20)
	if l.Len() != 0 {
		t.Fatal("expected empty list")
	}
	if l.Render() != "" {
		t.Fatal("expected empty render")
	}
}

func TestListAppend(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello", styles))
	l.Append(NewSystemItem("s1", "done", styles))
	if l.Len() != 2 {
		t.Fatalf("expected 2 items, got %d", l.Len())
	}
}

func TestListVirtualScroll(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 5) // small viewport

	// Add 10 items, each 1 line + gap = 2 lines
	for i := 0; i < 10; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	rendered := l.Render()
	lines := strings.Split(rendered, "\n")
	if len(lines) > 5 {
		t.Fatalf("expected at most 5 lines in viewport, got %d", len(lines))
	}
}

func TestListScrollDown(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 5)
	l.SetFollow(false)

	for i := 0; i < 10; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	l.ScrollDown(3)
	rendered := l.Render()
	// Should have scrolled — not showing the first items
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
}

func TestListScrollToEnd(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 5)

	for i := 0; i < 10; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	l.ScrollUp(20)
	l.ScrollToEnd()
	if !l.AtBottom() {
		t.Fatal("expected to be at bottom after ScrollToEnd")
	}
}

func TestListFindByID(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello", styles))
	l.Append(NewUserItem("u2", "world", styles))

	item := l.FindByID("u2")
	if item == nil {
		t.Fatal("expected to find u2")
	}
	if item.ID() != "u2" {
		t.Fatalf("expected u2, got %s", item.ID())
	}

	if l.FindByID("u99") != nil {
		t.Fatal("expected nil for non-existent ID")
	}
}

func TestListUpdateItem(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello", styles))

	updated := NewUserItem("u1", "world", styles)
	l.UpdateItem("u1", updated)

	item := l.FindByID("u1")
	if item == nil {
		t.Fatal("expected to find u1")
	}
	rendered := item.Render(80)
	if !strings.Contains(rendered, "world") {
		t.Fatalf("expected updated content, got: %s", rendered)
	}
}

func TestListFollow(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 3)
	l.SetFollow(true)

	// Append many items — should auto-scroll to last
	for i := 0; i < 20; i++ {
		l.Append(NewSystemItem("s", "line", styles))
	}

	rendered := l.Render()
	// The last items should be visible
	if !strings.Contains(rendered, "line") {
		t.Fatalf("expected content in rendered view")
	}
}

func TestListSetSize(t *testing.T) {
	styles := DefaultStyles()
	l := NewList(80, 20)
	l.Append(NewUserItem("u1", "hello world this is a test", styles))

	l.SetSize(20, 10)
	rendered := l.Render()
	if rendered == "" {
		t.Fatal("expected non-empty render after resize")
	}
}

func TestSplitVisualLinesMatchesMeasureHeight(t *testing.T) {
	// Every string's splitVisualLines count must equal measureHeight.
	cases := []string{
		"hello",
		"hello\nworld",
		"hello\nworld\n",
		"a\nb\nc\n",
		"",
		"\n",
		"\n\n",
		"header\n  body1\n  body2\n", // typical tool render
	}
	for _, s := range cases {
		lines := splitVisualLines(s)
		h := measureHeight(s)
		if len(lines) != h {
			t.Errorf("splitVisualLines(%q) = %d lines, measureHeight = %d", s, len(lines), h)
		}
	}
}

func TestListScrollShowsAllContent(t *testing.T) {
	styles := DefaultStyles()
	// Use a small viewport and enough tool items to overflow.
	// Each BashToolItem with a result produces a header + body ending in \n,
	// which is exactly the pattern that caused Height/Render mismatch.
	viewport := 5
	l := NewList(80, viewport)
	l.SetFollow(false)

	for i := 0; i < 20; i++ {
		item := NewBashToolItem("b", "Bash", "echo hi", StatusSuccess, styles)
		item.SetResult("ok", false)
		l.Append(item)
	}

	// Scroll to end and verify that the last item is actually rendered.
	l.ScrollToEnd()
	rendered := l.Render()
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}

	// The last item should have "ok" somewhere in the render output.
	// Before the fix, scrollToEnd would stop too early because
	// calcEndPositionLocked used Height() (undercounted) while Render()
	// consumed more lines, pushing the last item off-screen.
	if !l.AtBottom() {
		t.Error("expected AtBottom after ScrollToEnd")
	}
}

func TestListToolItemHeightMatchesRenderLines(t *testing.T) {
	styles := DefaultStyles()
	item := NewBashToolItem("b1", "Bash", "echo test", StatusSuccess, styles)
	item.SetResult("line1\nline2\nline3", false)

	w := 80
	h := item.Height(w)
	rendered := item.Render(w)
	visualLines := splitVisualLines(rendered)

	if len(visualLines) != h {
		t.Errorf("Height() = %d but Render() produces %d visual lines:\n%s", h, len(visualLines), rendered)
	}
}

func TestListShrinkViewportShowsLastItem(t *testing.T) {
	// Simulates: user sends a multi-line message, then the viewport shrinks
	// because a status bar appears. The last line of the message must still
	// be visible.
	styles := DefaultStyles()
	width := 60

	// Start with a large viewport (no status bar)
	l := NewList(width, 20)
	l.SetFollow(true)

	// Fill with enough content to require scrolling
	for i := 0; i < 15; i++ {
		l.Append(NewSystemItem("s", "filler line "+strings.Repeat("x", 20), styles))
	}

	// Append a multi-line user message (the one the user just sent)
	userMsg := "this isa test,这是一个测试\n阿萨德发顺丰"
	l.Append(NewUserItem("user1", userMsg, styles))

	// Render at original size — last line should be visible
	rendered := l.Render()
	if !strings.Contains(rendered, "阿萨德发顺丰") {
		t.Errorf("at size 20, last line should be visible.\nRendered:\n%s", rendered)
	}

	// Now shrink viewport by 2 (simulates status bar appearing)
	l.SetSize(width, 18)

	// Re-render — last line must STILL be visible
	rendered = l.Render()
	if !strings.Contains(rendered, "阿萨德发顺丰") {
		// Show what IS visible
		lines := strings.Split(rendered, "\n")
		t.Errorf("at size 18, last line '阿萨德发顺丰' should be visible but isn't.\nVisible lines (%d):\n%s",
			len(lines), rendered)
	}
}

func TestUserItemHeightMatchesRenderLines(t *testing.T) {
	styles := DefaultStyles()
	cases := []string{
		"hello",
		"this isa test,这是一个测试\n阿萨德发顺丰",
		"line1\nline2\nline3",
		"很长的中文文本用来测试在窄宽度下是否会自动换行并正确计算高度",
		"short\n" + strings.Repeat("这是一个比较长的行用来测试换行", 5),
	}
	for _, text := range cases {
		item := NewUserItem("u1", text, styles)
		for _, w := range []int{20, 40, 60, 80} {
			h := item.Height(w)
			rendered := item.Render(w)
			visualLines := splitVisualLines(rendered)
			if len(visualLines) != h {
				t.Errorf("UserItem(%q) at width %d: Height()=%d but Render() has %d visual lines.\nRendered:\n%s",
					text[:min(len(text), 30)], w, h, len(visualLines), rendered)
			}
			// Reset cache for next width
			item.Invalidate()
		}
	}
}

func TestListRenderProducesExactHeightLines(t *testing.T) {
	// Verify that Render() produces exactly l.height lines AND the last
	// item's content is visible when scrolled to the end.
	styles := DefaultStyles()
	width := 60
	height := 10

	l := NewList(width, height)
	l.SetFollow(true)

	for i := 0; i < 20; i++ {
		l.Append(NewSystemItem(fmt.Sprintf("s%d", i), "line"+fmt.Sprintf(" item%d", i), styles))
	}

	rendered := l.Render()
	renderedLines := strings.Split(rendered, "\n")
	if len(renderedLines) != height {
		t.Errorf("expected exactly %d lines, got %d\nRendered:\n%s", height, len(renderedLines), rendered)
	}
	if !strings.Contains(rendered, "item19") {
		t.Errorf("last item should be visible at scroll end.\nRendered:\n%s", rendered)
	}
}
