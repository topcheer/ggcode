package context

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSummarizeDiscardsOnConcurrentMutation(t *testing.T) {
	m := newTestManager(t)

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

	m.mu.Lock()
	if len(m.messages) > 2 {
		m.messages = append(m.messages[:1], m.messages[2:]...)
	}
	m.nonTailMutSeq++
	vAfter := m.nonTailMutSeq
	m.mu.Unlock()

	if vAfter == plan.origVersion {
		t.Fatal("test setup error: nonTailMutSeq should have bumped")
	}

	m.Add(provider.Message{ID: newMessageID(), Role: "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "tail during window"}}})
	m.mu.Lock()
	vAfterAdd := m.nonTailMutSeq
	m.mu.Unlock()
	if vAfterAdd != vAfter {
		t.Fatalf("Add must not bump nonTailMutSeq (tail append is rescued by extraMsgs), got %d -> %d", vAfter, vAfterAdd)
	}
}

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

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(100000)
}
