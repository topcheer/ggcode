package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// mustTextBlocks wraps text in provider text content blocks for fixtures.
func mustTextBlocks(texts ...string) []provider.ContentBlock {
	blocks := make([]provider.ContentBlock, 0, len(texts))
	for _, text := range texts {
		blocks = append(blocks, provider.ContentBlock{Type: "text", Text: text})
	}
	return blocks
}

// mustToolUseBlock returns a tool_use block that excerpt extraction must skip.
func mustToolUseBlock() []provider.ContentBlock {
	return []provider.ContentBlock{{Type: "tool_use", Input: []byte(`{"file_path":"a/b.go"}`)}}
}

func TestFirstTurnExcerpts(t *testing.T) {
	ses := session.NewSession("zai", "default", "test-model")
	ses.Messages = []provider.Message{
		{Role: "user", Content: mustTextBlocks("")}, // skipped: empty text
		{Role: "user", Content: mustTextBlocks("帮我看看这个报错")},
		{Role: "assistant", Content: append(mustTextBlocks("好的，这是空指针"), mustToolUseBlock()...)},
	}
	user, assistant := firstTurnExcerpts(ses)
	if user != "帮我看看这个报错" {
		t.Errorf("user excerpt = %q", user)
	}
	if assistant != "好的，这是空指针" {
		t.Errorf("assistant excerpt = %q (tool blocks must be excluded)", assistant)
	}
}

func TestHandleLLMTitleReadyMsgApplies(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ses := session.NewSession("zai", "default", "test-model")
	ses.Title = "hi"
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := Model{}
	m.session = ses
	m.sessionStore = store

	updated, _ := m.handleLLMTitleReadyMsg(llmTitleReadyMsg{title: "\"Fix login timeout bug\"", currentTitle: "hi"})
	if updated.session.Title != "Fix login timeout bug" {
		t.Errorf("title = %q, want sanitized LLM title", updated.session.Title)
	}
}

func TestHandleLLMTitleReadyMsgGuards(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ses := session.NewSession("zai", "default", "test-model")
	ses.Title = "hi"
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}

	t.Run("stale baseline dropped", func(t *testing.T) {
		m := Model{session: ses, sessionStore: store}
		updated, _ := m.handleLLMTitleReadyMsg(llmTitleReadyMsg{title: "Fix login bug", currentTitle: "New session"})
		if updated.session.Title != "hi" {
			t.Errorf("stale reply must be dropped, title = %q", updated.session.Title)
		}
	})
	t.Run("generic candidate rejected", func(t *testing.T) {
		m := Model{session: ses, sessionStore: store}
		updated, _ := m.handleLLMTitleReadyMsg(llmTitleReadyMsg{title: "hello", currentTitle: "hi"})
		if updated.session.Title != "hi" {
			t.Errorf("generic candidate must be rejected, title = %q", updated.session.Title)
		}
	})
	t.Run("nil session safe", func(t *testing.T) {
		m := Model{}
		updated, _ := m.handleLLMTitleReadyMsg(llmTitleReadyMsg{title: "Fix login bug", currentTitle: ""})
		if updated.session != nil {
			t.Error("nil session must be a no-op")
		}
	})
}
