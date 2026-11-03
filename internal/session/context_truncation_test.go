package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestContextTruncationKeepsDialogue(t *testing.T) {
	msgLine := func(role, text string, ts time.Time) string {
		return fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":%q,"content":[{"type":"text","text":%q}]}}`,
			ts.Format(time.RFC3339Nano), role, text)
	}

	now := time.Now()
	var b strings.Builder

	for i := 0; i < 30; i++ {
		ts := now.Add(-10 * time.Hour)
		b.WriteString(msgLine("user", fmt.Sprintf("question %d", i), ts) + "\n")
		b.WriteString(msgLine("assistant", fmt.Sprintf("answer %d", i), ts) + "\n")
	}
	for i := 0; i < 250; i++ {
		b.WriteString(msgLine("system", fmt.Sprintf("[note %d]", i), now.Add(-time.Duration(i)*time.Minute)) + "\n")
	}

	dir := t.TempDir()
	id := "20260818-100000-ctxguard000000000"
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}

	s := &JSONLStore{dir: dir, fullLoad: true}
	ses, err := s.loadSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ses.Messages) != 310 {
		t.Fatalf("messages = %d, want 310", len(ses.Messages))
	}
	if len(ses.ContextMessages) <= MaxContextMessages+1 {
		t.Fatalf("context messages = %d, expected > %d (window must extend past system tail)", len(ses.ContextMessages), MaxContextMessages+1)
	}

	var hasUser, hasAssistant bool
	for _, m := range ses.ContextMessages {
		if m.Role == "user" && strings.Contains(textOf(m), "question 29") {
			hasUser = true
		}
		if m.Role == "assistant" && strings.Contains(textOf(m), "answer 29") {
			hasAssistant = true
		}
	}
	if !hasUser || !hasAssistant {
		t.Fatalf("context window lost dialogue: hasUser=%v hasAssistant=%v (window %d msgs)", hasUser, hasAssistant, len(ses.ContextMessages))
	}

	if ses.ContextMessages[0].Role != "system" || !strings.Contains(textOf(ses.ContextMessages[0]), "truncated") {
		t.Fatalf("expected leading truncation note, got role=%q", ses.ContextMessages[0].Role)
	}
}

func textOf(m provider.Message) string {
	var sb strings.Builder
	for _, b := range m.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}
