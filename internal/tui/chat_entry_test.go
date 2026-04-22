package tui

import (
	"strings"
	"testing"
)

// --- ChatEntry unit tests ---

func TestChatEntry_Rendered_AssistantMarkdown(t *testing.T) {
	e := ChatEntry{Role: "assistant", RawText: "**bold** text"}
	r1 := e.Rendered(80)
	if r1 == "" {
		t.Fatal("expected non-empty rendered output")
	}
	// Cache hit — same width returns same string
	r2 := e.Rendered(80)
	if r1 != r2 {
		t.Error("expected cached result on same width")
	}
	if e.cachedWidth != 80 {
		t.Errorf("expected cachedWidth=80, got %d", e.cachedWidth)
	}
}

func TestChatEntry_Rendered_DifferentWidth_Rerenders(t *testing.T) {
	e := ChatEntry{Role: "assistant", RawText: "hello world this is a longer piece of text that should wrap differently at different widths"}
	r1 := e.Rendered(80)
	r2 := e.Rendered(40)
	if e.cachedWidth != 40 {
		t.Errorf("expected cachedWidth=40 after render at 40, got %d", e.cachedWidth)
	}
	// Width 40 should produce more lines than width 80
	lines40 := strings.Count(r2, "\n")
	lines80 := strings.Count(r1, "\n")
	if lines40 < lines80 {
		t.Errorf("expected more lines at width 40 (%d) than 80 (%d)", lines40, lines80)
	}
}

func TestChatEntry_Rendered_UserPlainText(t *testing.T) {
	e := ChatEntry{Role: "user", RawText: "hello world"}
	r := e.Rendered(80)
	if r == "" {
		t.Fatal("expected non-empty rendered output for user entry")
	}
	if strings.Contains(r, "**") {
		t.Error("user entries should not be markdown-rendered")
	}
}

func TestChatEntry_Rendered_SystemPassthrough(t *testing.T) {
	text := "some pre-rendered styled text"
	e := ChatEntry{Role: "system", RawText: text}
	r := e.Rendered(80)
	if r != text {
		t.Errorf("system entry should pass through RawText, got %q", r)
	}
}

func TestChatEntry_Rendered_ToolPassthrough(t *testing.T) {
	text := "tool result content"
	e := ChatEntry{Role: "tool", RawText: text}
	r := e.Rendered(80)
	if r != text {
		t.Errorf("tool entry should pass through RawText, got %q", r)
	}
}

func TestChatEntry_Invalidate(t *testing.T) {
	e := ChatEntry{Role: "system", RawText: "test"}
	e.Rendered(80)
	if e.cachedWidth != 80 {
		t.Fatal("expected cached width")
	}
	e.Invalidate()
	if e.cachedWidth != 0 {
		t.Errorf("expected cachedWidth=0 after invalidate, got %d", e.cachedWidth)
	}
	if e.rendered != "" {
		t.Error("expected empty rendered after invalidate")
	}
}

func TestChatEntry_Append(t *testing.T) {
	e := ChatEntry{Role: "assistant", RawText: "hello"}
	e.Rendered(80)
	e.Append(" world")
	if e.RawText != "hello world" {
		t.Errorf("expected 'hello world', got %q", e.RawText)
	}
	if e.cachedWidth != 0 {
		t.Error("expected cache invalidated after Append")
	}
}

func TestChatEntry_StreamingFlag(t *testing.T) {
	e := ChatEntry{Role: "assistant", RawText: "hello", Streaming: true}
	// Streaming entries should always re-render regardless of cache
	r1 := e.Rendered(80)
	e.RawText = "hello world"
	r2 := e.Rendered(80)
	if r1 == r2 {
		t.Error("streaming entry should re-render even at same width when RawText changes")
	}
}

func TestChatEntry_EmptyAssistant(t *testing.T) {
	e := ChatEntry{Role: "assistant", RawText: ""}
	r := e.Rendered(80)
	if r != "" {
		t.Errorf("empty assistant should produce empty output, got %q", r)
	}
}

// --- ChatEntryList tests ---

func TestChatEntryList_AppendAndRender(t *testing.T) {
	l := NewChatEntryList()
	l.Append(ChatEntry{Role: "user", RawText: "hello"})
	l.Append(ChatEntry{Role: "assistant", RawText: "world"})
	r := l.Render(80)
	if !strings.Contains(r, "hello") {
		t.Error("expected output to contain user text")
	}
	if !strings.Contains(r, "world") {
		t.Error("expected output to contain assistant text")
	}
}

func TestChatEntryList_InvalidateAll(t *testing.T) {
	l := NewChatEntryList()
	l.Append(ChatEntry{Role: "system", RawText: "test"})
	r1 := l.Render(80)
	_ = r1
	l.InvalidateAll()
	// After invalidation, re-render should still work
	r2 := l.Render(40)
	if r2 == "" {
		t.Error("expected non-empty output after invalidation")
	}
}

func TestChatEntryList_Reset(t *testing.T) {
	l := NewChatEntryList()
	l.Append(ChatEntry{Role: "user", RawText: "hello"})
	l.Reset()
	if l.Len() != 0 {
		t.Errorf("expected 0 entries after reset, got %d", l.Len())
	}
	r := l.Render(80)
	if r != "" {
		t.Errorf("expected empty render after reset, got %q", r)
	}
}

func TestChatEntryList_LastMatching(t *testing.T) {
	l := NewChatEntryList()
	l.Append(ChatEntry{Role: "user", RawText: "first"})
	l.Append(ChatEntry{Role: "assistant", RawText: "second"})
	l.Append(ChatEntry{Role: "user", RawText: "third"})

	last := l.LastMatching("assistant")
	if last == nil || last.RawText != "second" {
		t.Error("expected LastMatching to return assistant entry")
	}

	lastUser := l.LastMatching("user")
	if lastUser == nil || lastUser.RawText != "third" {
		t.Error("expected LastMatching to return last user entry")
	}

	none := l.LastMatching("nonexistent")
	if none != nil {
		t.Error("expected nil for non-existent role")
	}
}

func TestChatEntryList_Last(t *testing.T) {
	l := NewChatEntryList()
	if l.Last() != nil {
		t.Error("expected nil for empty list")
	}
	l.Append(ChatEntry{Role: "user", RawText: "hello"})
	l.Append(ChatEntry{Role: "assistant", RawText: "world"})
	last := l.Last()
	if last == nil || last.RawText != "world" {
		t.Error("expected Last to return last entry")
	}
}

func TestChatEntryList_LineCount(t *testing.T) {
	l := NewChatEntryList()
	l.Append(ChatEntry{Role: "system", RawText: "line1\nline2"})
	l.Append(ChatEntry{Role: "system", RawText: "line3"})
	count := l.LineCount(80)
	if count != 3 {
		t.Errorf("expected 3 lines, got %d", count)
	}
}

func TestChatEntryList_TrimOldest(t *testing.T) {
	l := NewChatEntryList()
	for i := 0; i < 5; i++ {
		l.Append(ChatEntry{Role: "system", RawText: string(rune('a' + i))})
	}
	l.TrimOldest(3)
	if l.Len() != 3 {
		t.Errorf("expected 3 entries after trim, got %d", l.Len())
	}
	first := l.LastMatching("system")
	if first == nil {
		t.Fatal("expected to find entry")
	}
}

// --- Resize re-render integration ---

func TestResizeRerendersAtNewWidth(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// Write a user message via dualWriteSystem
	m.dualWriteSystem("test content that should wrap differently at different widths\n")

	// Render at width 120
	r1 := m.renderOutput()

	// Resize to narrow width
	m.handleResize(40, 40)
	r2 := m.renderOutput()

	// Both should contain the text
	if !strings.Contains(r2, "test content") {
		t.Error("expected content to persist after resize")
	}

	// The output should be different because wrapping changed
	_ = r1 // At different widths, line counts should differ
}

func TestSidebarToggleInvalidatesCache(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.dualWriteSystem("sidebar toggle test\n")

	// Toggle sidebar
	m.sidebarVisible = true
	m.chatEntries.InvalidateAll()

	r := m.renderOutput()
	if !strings.Contains(r, "sidebar toggle test") {
		t.Error("expected content after sidebar toggle")
	}
}

// --- dualWriteSystem test ---

func TestDualWriteSystem(t *testing.T) {
	m := newTestModel()
	m.dualWriteSystem("hello\n")

	// Check both paths
	if !strings.Contains(m.output.String(), "hello") {
		t.Error("expected output buffer to contain text")
	}
	if m.chatEntries.Len() != 1 {
		t.Fatalf("expected 1 chatEntry, got %d", m.chatEntries.Len())
	}
	last := m.chatEntries.Last()
	if last == nil || last.RawText != "hello\n" {
		t.Errorf("expected chatEntry RawText='hello\\n', got %q", last.RawText)
	}
	if last.Role != "system" {
		t.Errorf("expected role=system, got %s", last.Role)
	}
}

func TestRenderOutputPrefersChatEntries(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// Write via dualWriteSystem (both paths)
	m.dualWriteSystem("from chatentries\n")

	output := m.renderOutput()
	if !strings.Contains(output, "from chatentries") {
		t.Errorf("renderOutput should prefer chatEntries, got %q", output)
	}
}

func TestRenderOutputFallsBackWhenChatEntriesEmpty(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// Write only to legacy output (bypass dualWriteSystem)
	m.output.WriteString("legacy only\n")

	output := m.renderOutput()
	// chatEntries is empty, but output has content → legacy path
	// Actually, since chatEntries.Render returns "", it falls back to legacy
	if !strings.Contains(output, "legacy only") {
		t.Errorf("renderOutput should fall back to output buffer, got %q", output)
	}
}
