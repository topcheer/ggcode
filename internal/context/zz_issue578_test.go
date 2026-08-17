package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue578_BugA_compositionLocked verifies that compositionLocked
// properly counts Cyrillic, Greek, and Latin-Extended characters in the
// CJK bucket for calibration (not invisible).
func TestIssue578_BugA_compositionLocked(t *testing.T) {
	// Create manager with Cyrillic text
	m := NewManager(4000)
	m.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "system"}},
	})
	m.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "Привет мир! Hello мир!"}}, // Russian + ASCII
	})

	messages := m.Messages()
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// Check composition via internal function
	ascii, cjk := m.compositionLocked()

	// "Привет мир! Hello мир!" breakdown:
	// ASCII: "system" (6) + "Hello" (5) + "!"×2 (2) + spaces (3) = 16 chars
	// Cyrillic: "Привет" (6) + "мир"×2 (6) = 12 chars — #598: NO LONGER in
	// the CJK calibration bucket. #578 originally folded Cyrillic in (the
	// tokenizer prices it 2.5 chars/token vs CJK 1.0), which pegged
	// cjkRatio to clamp after pure-Cyrillic sessions and underestimated
	// later Chinese by ~50%. Script classification itself (#535) is
	// unchanged — only calibration membership narrowed.
	if ascii != 16 {
		t.Errorf("expected 16 ASCII chars, got %d", ascii)
	}
	if cjk != 0 {
		t.Errorf("expected 0 CJK-bucket chars (Cyrillic excluded per #598), got %d", cjk)
	}

	t.Logf("composition: ascii=%d cjk=%d (Cyrillic excluded per #598)", ascii, cjk)
}

// TestIssue578_BugD_summaryProtection verifies that ApplyCompactResult
// preserves the summary message instead of overwriting it with live system.
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

	// Create a snapshot and compaction result with summary
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
		Changed:    true, // ApplyCompactResult rejects results with Changed=false
	}

	// Apply compaction - note: ApplyCompactResult has side effects
	// and requires hasLiveSystem to be false when we want to preserve the summary
	applied, _ := m.ApplyCompactResult(snapshot, result)
	// Note: ApplyCompactResult may return false if hasLiveSystem is true
	// but summary should still be preserved in the messages
	if !applied {
		t.Log("ApplyCompactResult returned false (may be expected with hasLiveSystem)")
	}

	// Verify summary is preserved
	msgs := m.Messages()
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 message after compaction")
	}

	// First message should still be the summary, not overwritten
	firstMsg := msgs[0]
	if !strings.Contains(firstMsg.Content[0].Text, "[Previous conversation summary]") {
		t.Errorf("summary was overwritten: got %q", firstMsg.Content[0].Text)
	}

	t.Log("summary preserved after ApplyCompactResult")
}
