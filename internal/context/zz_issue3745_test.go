package context

// #3745 probes: RemoveLastAssistantGroup must not irreversibly delete the
// assistant reply when there is no user text to re-submit, and must skip
// tool_result carriers (Role:"user") when scanning for the prompt.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue3745_EmptyUserTextDoesNotTruncate(t *testing.T) {
	m := NewManager(100000)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "image", ImageMIME: "image/png", ImageData: "cHJvYmU="},
	}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "the reply that must survive"},
	}})
	before := len(m.Messages())
	if got := m.RemoveLastAssistantGroup(); got != "" {
		t.Fatalf("image-only input must return empty, got %q", got)
	}
	if len(m.Messages()) != before {
		t.Fatal("no recoverable user text => assistant reply must NOT be deleted")
	}
}

func TestIssue3745_CarrierSkippedInUserScan(t *testing.T) {
	m := NewManager(100000)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: "list the files"},
	}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "tool_use", ToolID: "t1", ToolName: "list_directory"},
	}})
	// ReconcileToolCalls-style carrier: Role user, tool_result only.
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		provider.ToolResultBlock("t1", "file a\nfile b", false),
	}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "here are the files"},
	}})

	got := m.RemoveLastAssistantGroup()
	if got != "list the files" {
		t.Fatalf("carrier must be skipped, expected the real prompt, got %q", got)
	}
	// Truncation point: back to (and including) the real user prompt.
	if len(m.Messages()) != 1 {
		t.Fatalf("expected history truncated to the prompt, got %d messages", len(m.Messages()))
	}
}

func TestIssue3745_NormalPathUnchanged(t *testing.T) {
	m := NewManager(100000)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: "hello"},
	}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "hi"},
	}})
	if got := m.RemoveLastAssistantGroup(); got != "hello" {
		t.Fatalf("normal regenerate must keep returning the prompt, got %q", got)
	}
	if len(m.Messages()) != 1 {
		t.Fatal("normal path must still truncate")
	}
}
