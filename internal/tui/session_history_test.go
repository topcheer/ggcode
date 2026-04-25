package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestRestoreHistoryFromMessages(t *testing.T) {
	m := newTestModel()

	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "system prompt"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "帮我看看这个 bug"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "好的，我来看看"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "修复一下这个文件"}}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "  "}, // whitespace-only, should be skipped
			{Type: "text", Text: "这段有内容"},
		}},
	}

	m.restoreHistoryFromMessages(messages)

	if len(m.history) != 3 {
		t.Fatalf("expected 3 history entries, got %d: %#v", len(m.history), m.history)
	}
	if m.history[0] != "帮我看看这个 bug" {
		t.Errorf("history[0] = %q, want %q", m.history[0], "帮我看看这个 bug")
	}
	if m.history[1] != "修复一下这个文件" {
		t.Errorf("history[1] = %q, want %q", m.history[1], "修复一下这个文件")
	}
	if m.history[2] != "这段有内容" {
		t.Errorf("history[2] = %q, want %q", m.history[2], "这段有内容")
	}
	if m.historyIdx != 3 {
		t.Errorf("historyIdx = %d, want 3", m.historyIdx)
	}
}

func TestRestoreHistorySkipsEmptyAndNonText(t *testing.T) {
	m := newTestModel()

	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "image", Text: "base64data"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "   "}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "response"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "真实消息"}}},
	}

	m.restoreHistoryFromMessages(messages)

	if len(m.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d: %#v", len(m.history), m.history)
	}
	if m.history[0] != "真实消息" {
		t.Errorf("history[0] = %q, want %q", m.history[0], "真实消息")
	}
}

func TestRestoreHistoryResetsExisting(t *testing.T) {
	m := newTestModel()
	m.history = []string{"old command"}
	m.historyIdx = 1

	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "new prompt"}}},
	}

	m.restoreHistoryFromMessages(messages)

	if len(m.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d: %#v", len(m.history), m.history)
	}
	if m.history[0] != "new prompt" {
		t.Errorf("history[0] = %q, want %q", m.history[0], "new prompt")
	}
}

func TestHistoryNavigationAfterRestore(t *testing.T) {
	m := newTestModel()

	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "first prompt"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "second prompt"}}},
	}
	m.restoreHistoryFromMessages(messages)

	// historyIdx should be at end (2), so Up should go to most recent
	model, _ := m.handleHistoryUp()
	m2 := model.(Model)
	if m2.input.Value() != "second prompt" {
		t.Errorf("after Up: input = %q, want %q", m2.input.Value(), "second prompt")
	}

	// Another Up should go to first
	model, _ = m2.handleHistoryUp()
	m3 := model.(Model)
	if m3.input.Value() != "first prompt" {
		t.Errorf("after 2nd Up: input = %q, want %q", m3.input.Value(), "first prompt")
	}

	// Down should go back to second
	model, _ = m3.handleHistoryDown()
	m4 := model.(Model)
	if m4.input.Value() != "second prompt" {
		t.Errorf("after Down: input = %q, want %q", m4.input.Value(), "second prompt")
	}
}
