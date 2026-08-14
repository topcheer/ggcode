package chat

import (
	"strings"
	"testing"
)

// TestStreamingBodyHeightMatchesRenderLines pins the contract that caused
// display corruption: during live command output (SetStreamingBody), the
// item's Height() must equal the number of visual lines List.Render will
// actually slice. Height() counts width-wrapped lines (measureHeightWidth),
// while List.Render splits physical lines — the streaming body MUST be
// width-wrapped by RenderBody or the two disagree and scrolling produces
// blank-line artifacts / skipped lines.
func TestStreamingBodyHeightMatchesRenderLines(t *testing.T) {
	styles := DefaultStyles()
	item := NewBaseToolItem("t1", "wait_command", StatusRunning, "", styles)
	item.SetStreamingBody(strings.Repeat("x", 300)) // one 300-char line, width 80

	const width = 80
	rendered := item.RenderBody(width)
	// The invariant: the width-aware count used by Height() must equal the
	// physical line count used by List.Render's splitVisualLines. Before the
	// fix, the 300-char single line counted as 4 wrapped lines in Height but
	// stayed 1 physical line in Render.
	wrapped := measureHeightWidth(rendered, width)
	physical := strings.Split(rendered, "\n")
	if wrapped != len(physical) {
		t.Fatalf("width-aware count=%d but physical lines=%d — streaming body is not width-wrapped", wrapped, len(physical))
	}
	if len(physical) < 4 {
		t.Fatalf("300-char line at width 80 must wrap to >=4 lines, got %d", len(physical))
	}
}

// TestStreamingBodyCappedToMaxLines ensures a large live tail (agent passed
// a big tail_lines) is bounded, same as the final result body.
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
	if n > ToolBodyMaxLines+1 { // +1 for the "… N more lines" marker
		t.Fatalf("streaming body must cap at %d lines + marker, got %d", ToolBodyMaxLines, n-1)
	}
	if !strings.Contains(body, "more lines") {
		t.Fatal("expected truncation marker for oversized streaming body")
	}
	// Newest output survives: streaming shows the tail.
	if !strings.Contains(body, "short") {
		t.Fatal("expected kept tail content in streaming body")
	}
}

// TestStreamingBodyShortPassthrough: normal small tails render identically
// to the old raw path (no marker, no truncation).
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
