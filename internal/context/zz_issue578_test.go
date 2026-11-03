package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue578_BugA_compositionLocked(t *testing.T) {
	m := NewManager(4000)
	m.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "system"}},
	})
	m.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "Привет мир! Hello мир!"}},
	})

	messages := m.Messages()
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	ascii, cjk, latinExt := m.compositionLocked()

	if ascii != 16 {
		t.Errorf("expected 16 ASCII chars, got %d", ascii)
	}
	if cjk != 0 {
		t.Errorf("expected 0 CJK-bucket chars (Cyrillic excluded per #598), got %d", cjk)
	}
	if latinExt != 0 {
		t.Errorf("expected 0 Latin-Extended chars, got %d", latinExt)
	}

	t.Logf("composition: ascii=%d cjk=%d (Cyrillic excluded per #598)", ascii, cjk)
}

func TestIssue578_BugD_summaryProtection(t *testing.T) {
	m := NewManager(4000)
	system := "system prompt"
	m.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: system}},
	})
	m.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "hello"}},
	})
	m.Add(provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "hi"}},
	})

	snapshot := m.CompactSnapshot()
	summaryMsg := provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "[Previous conversation summary] Summary text here"},
		},
	}
	result := CompactResult{
		Messages:   []provider.Message{summaryMsg},
		TokenCount: 100,
		Changed:    true,
	}

	applied, _ := m.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Log("ApplyCompactResult returned false (may be expected with hasLiveSystem)")
	}

	msgs := m.Messages()
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 message after compaction")
	}

	firstMsg := msgs[0]
	if !strings.Contains(firstMsg.Content[0].Text, "[Previous conversation summary]") {
		t.Errorf("summary was overwritten: got %q", firstMsg.Content[0].Text)
	}

	t.Log("summary preserved after ApplyCompactResult")
}
