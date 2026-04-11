package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestResumeSessionRebuildsConversationOutput(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	ses := session.NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Replay me"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := NewModel(agent.NewAgent(nil, nil, "", 0), nil)
	m.SetSession(session.NewSession("zai", "cn-coding-openai", "glm-5-turbo"), store)
	m.output.WriteString("stale output")

	cmd := m.resumeSession(ses.ID)
	if cmd == nil {
		t.Fatal("expected resumeSession command")
	}
	next, followup := m.Update(cmd())
	if followup != nil {
		t.Fatal("expected resume stream message to finish inline")
	}
	updated := next.(Model)
	output := updated.output.String()
	if output == "stale output" {
		t.Fatal("expected resume to rebuild conversation output")
	}
	if !containsAll(output, "❯ hello", "world", ses.ID) {
		t.Fatalf("unexpected rebuilt output: %q", output)
	}
}

func TestRenderConversationMessageIncludesToolBlocks(t *testing.T) {
	m := newTestModel()
	m.rebuildConversationFromMessages([]provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "I used a tool."},
			{Type: "tool_use", ToolID: "tool-1", ToolName: "read_file", Input: []byte(`{"path":"README.md"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "tool-1", ToolName: "read_file", Output: "line1\nline2", IsError: false},
		}},
	})
	output := m.output.String()
	if !containsAll(output, "I used a tool.", "Read README.md", "done: 2 lines of content") {
		t.Fatalf("unexpected rendered output: %q", output)
	}
	if strings.Contains(output, "Tool Call:") || strings.Contains(output, `"path":"README.md"`) {
		t.Fatalf("expected resume renderer to avoid raw tool markdown dump, got %q", output)
	}
}

func TestResumeSessionRendersCommandToolsCompactly(t *testing.T) {
	m := newTestModel()
	m.rebuildConversationFromMessages([]provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "tool-1", ToolName: "run_command", Input: []byte(`{"command":"# Check status\ngit status --short\nhead -5\n"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "tool-1", ToolName: "run_command", Output: "M README.md\nA internal/tui/view.go\n", IsError: false},
		}},
	})
	output := m.output.String()
	if !containsAll(output, "Check status", "git status --short", "M README.md", "A internal/tui/view.go") {
		t.Fatalf("unexpected rendered output: %q", output)
	}
	if strings.Contains(output, `{"command":`) || strings.Contains(output, "# Check status") {
		t.Fatalf("expected resume command renderer to avoid raw JSON/comment dump, got %q", output)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
