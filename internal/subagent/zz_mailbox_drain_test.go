package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestDrainMailboxConsumesPendingInOrder(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	id := m.Spawn("tester", "task", "task", nil, context.Background())
	for i, body := range []string{"m1", "m2", "m3"} {
		if err := m.SendToAgent(id, AgentMessage{From: "main", Message: body, Summary: string(rune('a' + i))}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	drained := m.DrainMailbox(id)
	if len(drained) != 3 {
		t.Fatalf("want 3 drained, got %d", len(drained))
	}
	for i, want := range []string{"m1", "m2", "m3"} {
		if drained[i].Message != want {
			t.Errorf("drained[%d] = %q, want %q (FIFO order broken)", i, drained[i].Message, want)
		}
	}
	if again := m.DrainMailbox(id); len(again) != 0 {
		t.Fatalf("consume-once broken: second drain returned %d", len(again))
	}
}

func TestDrainMailboxKeepsFullMailboxSemantics(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	id := m.Spawn("tester", "task", "task", nil, context.Background())
	fullErr := ""
	for i := 0; i < 32; i++ {
		if err := m.SendToAgent(id, AgentMessage{From: "main", Message: "fill"}); err != nil {
			fullErr = err.Error()
			break
		}
	}
	if fullErr == "" || !strings.Contains(fullErr, "mailbox is full") {
		t.Fatalf("full-mailbox error semantics regressed: %q", fullErr)
	}
	sa, ok := m.Get(id)
	if !ok {
		t.Fatal("sub-agent missing after Spawn")
	}
	if drained := m.DrainMailbox(id); len(drained) != cap(sa.Mailbox) {
		t.Fatalf("want cap(%d) drained, got %d", cap(sa.Mailbox), len(drained))
	}
	if err := m.SendToAgent(id, AgentMessage{From: "main", Message: "after drain"}); err != nil {
		t.Fatalf("send after drain should succeed, got %v", err)
	}
}

func TestDrainMailboxUnknownIDNoop(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	if got := m.DrainMailbox("nope"); got != nil {
		t.Fatalf("unknown id must drain nil, got %v", got)
	}
}

type mailboxFeedCapturingRunner struct {
	feed  func() []AgentMessage
	wired bool
}

func (r *mailboxFeedCapturingRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	return nil
}

func (r *mailboxFeedCapturingRunner) SetMailboxFeed(fn func() []AgentMessage) {
	r.feed = fn
	r.wired = true
}

// TestRunWiresMailboxFeedIntoAgentLoop pins the runner wiring: Run must
// install a non-blocking mailbox drain into the sub-agent's agent instance
// so send_message deliveries surface as conversation input instead of dead
// letters.
func TestRunWiresMailboxFeedIntoAgentLoop(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	id := m.Spawn("tester", "task body", "task body", nil, context.Background())
	fake := &mailboxFeedCapturingRunner{}
	Run(context.Background(), RunnerConfig{
		Task:    "task body",
		Manager: func() *Manager { return m }(),
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return fake
		},
		SubAgentID: id,
	})
	if !fake.wired || fake.feed == nil {
		t.Fatal("Run must wire SetMailboxFeed into the sub-agent's agent loop")
	}
	if err := m.SendToAgent(id, AgentMessage{From: "main", Message: "steer left", Summary: "redirect"}); err != nil {
		t.Fatalf("send after run: %v", err)
	}
	msgs := fake.feed()
	if len(msgs) != 1 || msgs[0].Message != "steer left" || msgs[0].From != "main" || msgs[0].Summary != "redirect" {
		t.Fatalf("feed drain wrong: %+v", msgs)
	}
	if again := fake.feed(); len(again) != 0 {
		t.Fatalf("feed must be consume-once, got %d on second call", len(again))
	}
}
