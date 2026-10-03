package agent

// Regression probe for #3178: the package-level session budget sync.Maps
// keyed by *Agent pinned the agent (and its whole reference graph) forever
// - Close must Delete both entries so daemon-style per-request agents are
// collectable.

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newTestAgent3178(t *testing.T) *Agent {
	t.Helper()
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
		},
	}
	return NewAgent(mp, tool.NewRegistry(), "", 1)
}

func TestIssue3178_CloseReleasesBudgetStores(t *testing.T) {
	a := newTestAgent3178(t)
	a.SetSessionTimeBudget(10 * time.Minute)
	if got := a.SessionTimeBudget(); got != 10*time.Minute {
		t.Fatalf("budget not set: %v", got)
	}
	a.Close()
	// After Close the state entry must be GONE - a fresh lookup returns a
	// brand-new zero state, observable as budget 0 (not the configured 10m).
	if got := a.SessionTimeBudget(); got != 0 {
		t.Fatalf("time budget state survived Close (leak, #3178): %v", got)
	}
	// Token dimension released together (same storage pattern).
	if got := a.SessionTokenBudget(); got != 0 {
		t.Fatalf("token budget state survived Close (leak, #3178): %v", got)
	}
}

func TestIssue3178_CloseIdempotentForBudgetStores(t *testing.T) {
	a := newTestAgent3178(t)
	a.Close()
	a.Close() // second Close must not panic or resurrect state
	if got := a.SessionTimeBudget(); got != 0 {
		t.Fatalf("double Close left state: %v", got)
	}
}
