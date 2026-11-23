package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestSetSessionIDNilRegistrySafe(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}}},
		},
	}
	a := NewAgent(mp, nil, "", 1)
	if a.tools != nil {
		t.Fatal("fixture must actually carry a nil registry")
	}
	a.SetSessionID("sess-4513")
	if a.sessionID != "sess-4513" {
		t.Fatalf("sessionID must still be set, got %q", a.sessionID)
	}

	b := NewAgent(mp, tool.NewRegistry(), "", 1)
	b.SetSessionID("sess-b")
	if b.sessionID != "sess-b" {
		t.Fatalf("non-nil registry path regressed, got %q", b.sessionID)
	}
}
