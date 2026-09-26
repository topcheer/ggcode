package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newCompactTestAgent(t *testing.T) *Agent {
	t.Helper()
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)
	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	return a
}

func TestRequestAutoCompactNilContextManager(t *testing.T) {
	a := newCompactTestAgent(t)
	a.SetContextManager(nil)
	got := a.RequestAutoCompact("")
	if !strings.Contains(got, "unavailable") {
		t.Fatalf("expected unavailable status, got: %q", got)
	}
}

func TestRequestAutoCompactBelowThresholdIsHonest(t *testing.T) {
	a := newCompactTestAgent(t)
	a.SetContextManager(ctxpkg.NewManager(200000))
	got := a.RequestAutoCompact("test boundary")
	if got == "" {
		t.Fatal("status must be non-empty")
	}
	// An empty context is far below the auto-compact threshold: the status
	// must NOT claim a full summarization was scheduled (honesty prevents
	// the model from retrying or assuming context was summarized).
	if strings.Contains(got, "scheduled in the background") {
		t.Fatalf("must not claim full summarization below threshold: %q", got)
	}
	if !strings.Contains(got, "below the auto-compact threshold") {
		t.Fatalf("expected below-threshold status, got: %q", got)
	}
}
