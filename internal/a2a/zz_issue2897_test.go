package a2a

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
)

// zz_issue2897_test.go - regression probe for #2897: a resumed
// input-required task must pass the FULL conversation history to the fresh
// stateless agent. The old executeAgent received only the LAST message -
// a short follow-up like "yes" reached a brand-new instance with zero
// context about what was being confirmed.

// captureProvider records the prompt (last user message) of each call.
type captureProvider struct {
	scriptProvider
	mu      sync.Mutex
	prompts []string
}

func (p *captureProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	for _, m := range messages {
		if m.Role == "user" {
			for _, blk := range m.Content {
				if blk.Type == "text" && blk.Text != "" {
					p.prompts = append(p.prompts, blk.Text)
					break
				}
			}
			break
		}
	}
	p.mu.Unlock()
	return p.scriptProvider.ChatStream(ctx, messages, tools)
}

func (p *captureProvider) lastPrompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.prompts) == 0 {
		return ""
	}
	return p.prompts[len(p.prompts)-1]
}

func TestIssue2897ResumePromptCarriesFullHistory(t *testing.T) {
	reg := newStubRegistry()
	cp := &captureProvider{scriptProvider: scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "done"}},
	}}}
	a := agent.NewAgent(cp, reg, "test", 5)
	h := newHandler(a, reg)

	taskID := "task-2897"
	task := &Task{
		ID:        taskID,
		Skill:     SkillCodeEdit,
		ContextID: "ctx",
		History: []Message{
			{Role: "user", MessageID: "m1", Parts: []Part{{Kind: "text", Text: "edit internal/util/helpers.go to add a Retry wrapper"}}},
			{Role: "agent", Parts: []Part{{Kind: "text", Text: "Should the retry use exponential backoff?"}}},
		},
		Status: TaskStatus{State: TaskStateInputRequired, Timestamp: time.Now()},
	}
	h.mu.Lock()
	h.tasks[taskID] = task
	h.mu.Unlock()

	follow := Message{Role: "user", MessageID: "m2", Parts: []Part{{Kind: "text", Text: "yes"}}}
	if _, err := h.continueTask(context.Background(), taskID, follow); err != nil {
		t.Fatalf("continueTask: %v", err)
	}

	// Wait for the resumed execute goroutine to reach the agent call.
	deadline := time.Now().Add(5 * time.Second)
	prompt := ""
	for time.Now().Before(deadline) {
		if prompt = cp.lastPrompt(); prompt != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if prompt == "" {
		t.Fatal("timed out waiting for the agent call")
	}

	// The prompt must carry the original instruction and the agent question,
	// not just the bare "yes".
	if !strings.Contains(prompt, "edit internal/util/helpers.go") {
		t.Fatalf("#2897: prompt lost the original task instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "exponential backoff") {
		t.Fatalf("#2897: prompt lost the agent's input-required question: %q", prompt)
	}
	if !strings.Contains(prompt, "yes") {
		t.Fatalf("#2897: prompt must still contain the follow-up text: %q", prompt)
	}

	// Single-turn tasks: no transcript prefix machinery, prompt is the
	// plain skill-wrapped message (no behavioral regression).
	single := &captureProvider{scriptProvider: scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "ok"}},
	}}}
	h2a := agent.NewAgent(single, reg, "test", 5)
	h2 := newHandler(h2a, reg)
	if _, err := h2.Handle(context.Background(), SkillCodeEdit,
		Message{MessageID: "s1", Parts: []Part{{Kind: "text", Text: "rename foo to bar"}}}, ""); err != nil {
		t.Fatalf("single-turn handle: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	prompt = ""
	for time.Now().Before(deadline) {
		if prompt = single.lastPrompt(); prompt != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if prompt == "" {
		t.Fatal("timed out waiting for single-turn agent call")
	}
	if strings.Contains(prompt, "Conversation history") {
		t.Fatalf("single-turn prompt must not carry transcript prefix: %q", prompt)
	}
	if !strings.Contains(prompt, "rename foo to bar") {
		t.Fatalf("single-turn prompt lost the instruction: %q", prompt)
	}
}
