package context

// #3797 probe: the removed-count in RemoveLastAssistantGroup's debug log
// is computed BEFORE truncation. Functional pin: with a 6-message history
// the truncation removes exactly oldLen-(lastUserIdx+1) messages; the
// precomputed `removed` variable carries that same value into the log.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue3797_TruncationRemovesFullTailCount(t *testing.T) {
	m := NewManager(100000)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "go"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "r1"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.ToolResultBlock("t1", "o", false)}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.ToolResultBlock("t2", "o", false)}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "r2"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.ToolResultBlock("t3", "o", false)}})
	oldLen := len(m.Messages())

	if got := m.RemoveLastAssistantGroup(); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	remaining := len(m.Messages())
	wantRemoved := oldLen - remaining // exactly what the log must report
	if wantRemoved != 5 {             // r1 + two carriers + r2 + trailing carrier (carriers skip in the user scan)
		t.Fatalf("fixture: expected 5 removed messages, math says %d", wantRemoved)
	}
	if remaining != 1 { // only the real user prompt survives
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}
