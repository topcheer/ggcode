package chat

import (
	"strings"
	"testing"
)

func TestStreamingBodyHeightMatchesRenderLines(t *testing.T) {
	styles := DefaultStyles()
	item := NewBaseToolItem("t1", "wait_command", StatusRunning, "", styles)
	item.SetStreamingBody(strings.Repeat("x", 300))

	const width = 80
	rendered := item.RenderBody(width)

	wrapped := measureHeightWidth(rendered, width)
	physical := strings.Split(rendered, "\n")
	if wrapped != len(physical) {
		t.Fatalf("width-aware count=%d but physical lines=%d — streaming body is not width-wrapped", wrapped, len(physical))
	}
	if len(physical) < 4 {
		t.Fatalf("300-char line at width 80 must wrap to >=4 lines, got %d", len(physical))
	}
}

func TestStreamingBodyCappedToMaxLines(t *testing.T) {
	styles := DefaultStyles()
	item := NewBaseToolItem("t2", "wait_command", StatusRunning, "", styles)

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "short")
	}
	item.SetStreamingBody(strings.Join(lines, "\n"))

	body := item.RenderBody(80)
	n := strings.Count(body, "\n") + 1
	if n > ToolBodyMaxLines+1 {
		t.Fatalf("streaming body must cap at %d lines + marker, got %d", ToolBodyMaxLines, n-1)
	}
	if !strings.Contains(body, "more lines") {
		t.Fatal("expected truncation marker for oversized streaming body")
	}

	if !strings.Contains(body, "short") {
		t.Fatal("expected kept tail content in streaming body")
	}
}

func TestStreamingBodyShortPassthrough(t *testing.T) {
	styles := DefaultStyles()
	item := NewBaseToolItem("t3", "wait_command", StatusRunning, "", styles)
	body := "[...] compiling…\n[...] 42% "
	item.SetStreamingBody(body)

	got := item.RenderBody(80)
	if got == "" || !strings.Contains(got, "[...] compiling") {
		t.Fatalf("expected short streaming body rendered, got %q", got)
	}
	if strings.Contains(got, "more lines") {
		t.Fatal("short body must not be truncated")
	}
}
