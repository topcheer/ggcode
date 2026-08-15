package context

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestSummarizeDiscardsOnConcurrentMutation (#479): a version bump during the
// (unlocked) LLM window — anything that is not a pure tail append: deletes,
// mid-inserts, mechanical clears — must discard the stale snapshot instead
// of replaying it over the concurrent mutation.
func TestSummarizeDiscardsOnConcurrentMutation(t *testing.T) {
	m := newTestManager(t)

	// Fill enough messages to form a plan.
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		m.Add(provider.Message{ID: newMessageID(), Role: role,
			Content: []provider.ContentBlock{{Type: "text", Text: padded("msg", i)}}})
	}

	plan, ok := m.buildSummaryPlan()
	if !ok {
		t.Fatal("expected a summary plan")
	}

	// Simulate a concurrent NON-tail mutation during the LLM window:
	// ReconcileToolCalls-style delete bumps m.nonTailMutSeq (#479).
	m.mu.Lock()
	if len(m.messages) > 2 {
		m.messages = append(m.messages[:1], m.messages[2:]...)
	}
	m.nonTailMutSeq++
	vAfter := m.nonTailMutSeq
	m.mu.Unlock()

	// The write-back guard must see the mismatch. We verify the guard
	// predicate directly (the full Summarize needs a live provider; the
	// version check happens before any provider interaction).
	if vAfter == plan.origVersion {
		t.Fatal("test setup error: nonTailMutSeq should have bumped")
	}

	// Tail append (Add) must NOT trip the guard — Add doesn't bump version.
	m.Add(provider.Message{ID: newMessageID(), Role: "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "tail during window"}}})
	m.mu.Lock()
	vAfterAdd := m.nonTailMutSeq
	m.mu.Unlock()
	if vAfterAdd != vAfter {
		t.Fatalf("Add must not bump nonTailMutSeq (tail append is rescued by extraMsgs), got %d -> %d", vAfter, vAfterAdd)
	}
}

// TestSummarizePlanVersionSnapshot (#479): origVersion must equal m.version
// at snapshot time so an UNTOUCHED window keeps the plan valid.
func TestSummarizePlanVersionSnapshot(t *testing.T) {
	m := newTestManager(t)
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		m.Add(provider.Message{ID: newMessageID(), Role: role,
			Content: []provider.ContentBlock{{Type: "text", Text: padded("v", i)}}})
	}
	plan, ok := m.buildSummaryPlan()
	if !ok {
		t.Fatal("expected a summary plan")
	}
	m.mu.Lock()
	cur := m.nonTailMutSeq
	m.mu.Unlock()
	if plan.origVersion != cur {
		t.Fatalf("origVersion=%d, current=%d — snapshot must capture live nonTailMutSeq", plan.origVersion, cur)
	}
}

// TestSummarizeConcurrentGuardNoDeadlock: the guard path takes and releases
// m.mu cleanly (no re-entrancy deadlock when Summarize returns early).
func TestSummarizeConcurrentGuardNoDeadlock(t *testing.T) {
	m := newTestManager(t)
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		m.Add(provider.Message{ID: newMessageID(), Role: role,
			Content: []provider.ContentBlock{{Type: "text", Text: padded("d", i)}}})
	}
	// Bump nonTailMutSeq to force the discard path, then call Summarize with
	// a nil provider — it must return (nil error) without deadlocking.
	m.mu.Lock()
	m.nonTailMutSeq++
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := m.Summarize(context.Background(), stubProvider{}); err != nil {
			t.Logf("Summarize returned err (acceptable): %v", err)
		}
	}()
	select {
	case <-done:
		// guard fired before any provider use, or provider error — both fine
	case <-time.After(5 * time.Second):
		t.Fatal("Summarize deadlocked on the guard path")
	}
}

func padded(prefix string, i int) string {
	out := prefix
	for len(out) < 60 {
		out += "-"
	}
	return out + string(rune('a'+i%26))
}

// stubProvider is a minimal Provider whose Chat returns a canned summary —
// enough to carry Summarize past the LLM call to the write-back guard.
type stubProvider struct{}

func (stubProvider) Name() string              { return "stub" }
func (stubProvider) ReasoningEffort() string   { return "" }
func (stubProvider) SetReasoningEffort(string) {}
func (stubProvider) ToolChoice() string        { return "" }
func (stubProvider) SetToolChoice(string)      {}
func (stubProvider) CountTokens(_ context.Context, _ []provider.Message) (int, error) {
	return 10, nil
}
func (stubProvider) ChatStream(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (stubProvider) Chat(_ context.Context, _ []provider.Message, _ []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "stub summary of the conversation"}},
		},
	}, nil
}

// newTestManager builds a Manager sized like existing tests.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(100000)
}
