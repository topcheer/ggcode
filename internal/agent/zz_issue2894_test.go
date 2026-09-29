package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// zz_issue2894_test.go - regression probe for #2894: after the truncation
// continuation budget is exhausted (3 auto-continues), a FOURTH truncated
// response used to complete the run silently - the partial output was
// indistinguishable from a complete answer. The fix emits a
// StreamEventSystem notice on that path, mirroring the policyBlocked notice
// and the #1672 empty-response abort.
func TestIssue2894TruncationBudgetExhaustedNotifies(t *testing.T) {
	truncatedStream := []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "partial output that keeps getting cut"},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 100}, Truncated: true},
	}
	// 3 truncated responses burn the continuation budget; the 4th truncated
	// response is the budget-exhausted path under test.
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			truncatedStream, truncatedStream, truncatedStream, truncatedStream,
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), t.TempDir(), 10)
	defer a.Close()

	var systemTexts []string
	err := a.RunStream(context.Background(), "write something very long", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventSystem {
			systemTexts = append(systemTexts, event.Text)
		}
	})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	// Exactly 4 stream calls: 3 auto-continues, then the 4th truncated
	// response is kept as-is (no 5th call).
	if got := mp.streamCallCount(); got != 4 {
		t.Fatalf("expected 4 stream calls (3 continuations + exhausted keep), got %d", got)
	}
	joined := strings.Join(systemTexts, "\n")
	if !strings.Contains(joined, "budget exhausted") {
		t.Fatalf("#2894: expected budget-exhausted notice after 3 auto-continues, got system events: %q", joined)
	}
	if !strings.Contains(joined, "may be incomplete") {
		t.Fatalf("#2894: notice must say the response may be incomplete, got: %q", joined)
	}
}
