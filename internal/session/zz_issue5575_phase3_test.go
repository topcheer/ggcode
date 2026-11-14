package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Phase-3 pin (#5575): RecentMessages tail correctness.
func TestIssue5575Phase3RecentMessagesTail(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	var sb strings.Builder
	ses := Session{ID: "p3test1"}
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		ses.Messages = append(ses.Messages, provider.Message{
			Role:    role,
			Content: []provider.ContentBlock{{Type: "text", Text: "turn-" + string(rune('a'+i))}},
		})
	}
	b, _ := json.Marshal(&ses)
	sb.Write(b)
	sb.WriteString("\n")
	if err := os.WriteFile(filepath.Join(dir, "p3test1.jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := st.RecentMessages("p3test1", 5)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	if got[0].Content != "turn-h" || got[4].Content != "turn-l" {
		t.Fatalf("tail wrong: first=%q last=%q", got[0].Content, got[4].Content)
	}
	if got[0].Role != "assistant" {
		t.Fatalf("role = %q, want assistant", got[0].Role)
	}
}

// Phase-3 pin (#5575): missing file / invalid id = empty face, not error.
func TestIssue5575Phase3RecentMessagesAbsent(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := st.RecentMessages("nonexistent", 8); len(got) != 0 {
		t.Fatalf("missing file: got %d msgs, want 0", len(got))
	}
	if got := st.RecentMessages("../escape", 8); len(got) != 0 {
		t.Fatalf("invalid id: got %d msgs, want 0", len(got))
	}
}
