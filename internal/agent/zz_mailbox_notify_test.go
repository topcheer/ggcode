package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

func newMailboxTestAgent(t *testing.T) *Agent {
	t.Helper()
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
			Usage: provider.TokenUsage{InputTokens: 5, OutputTokens: 1},
		},
	}
	ag := NewAgent(mp, tool.NewRegistry(), "system", 5)
	if ag == nil {
		t.Fatal("NewAgent returned nil")
	}
	return ag
}

func mailboxUserTexts(ag *Agent) []string {
	var out []string
	for _, m := range ag.contextManager.Messages() {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "Message(s) arrived") {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

func TestMailboxNotifyNoFeedNoop(t *testing.T) {
	ag := newMailboxTestAgent(t)
	if ag.injectMailboxMessages() {
		t.Fatal("no feed installed: must be a no-op")
	}
}

func TestMailboxNotifyEmptyFeedNoop(t *testing.T) {
	ag := newMailboxTestAgent(t)
	ag.SetMailboxFeed(func() []subagent.AgentMessage { return nil })
	if ag.injectMailboxMessages() {
		t.Fatal("empty drain must not inject")
	}
	if len(mailboxUserTexts(ag)) != 0 {
		t.Fatal("empty drain injected content")
	}
}

func TestMailboxNotifyInjectsPendingAsUserRole(t *testing.T) {
	ag := newMailboxTestAgent(t)
	ag.SetMailboxFeed(func() []subagent.AgentMessage {
		return []subagent.AgentMessage{
			{From: "main", Message: "pivot: use option B", Summary: "redirect"},
			{From: "reviewer", Message: "stop after the unit tests"},
		}
	})
	if !ag.injectMailboxMessages() {
		t.Fatal("pending mailbox messages must inject")
	}
	texts := mailboxUserTexts(ag)
	if len(texts) != 1 {
		t.Fatalf("want exactly one merged injection, got %d", len(texts))
	}
	txt := texts[0]
	for _, want := range []string{
		"from main", "redirect", "pivot: use option B",
		"from reviewer", "stop after the unit tests",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("injected block missing %q", want)
		}
	}

	// Consume-once: the channel drain has no baseline/dedup swallow, so a
	// later drain delivering new messages must inject again immediately.
	ag.SetMailboxFeed(func() []subagent.AgentMessage {
		return []subagent.AgentMessage{{From: "main", Message: "second batch"}}
	})
	if !ag.injectMailboxMessages() {
		t.Fatal("second drain must inject again (consume-once, no baseline swallow)")
	}
	if got := len(mailboxUserTexts(ag)); got != 2 {
		t.Fatalf("want 2 injections total, got %d", got)
	}
}

// TestConversationLoopDrainsMailboxEachIteration pins the loop wiring: the
// per-iteration injection block must drain the mailbox alongside the
// subagent-completions injection (same pattern as the #4515/#4516 pins).
func TestConversationLoopDrainsMailboxEachIteration(t *testing.T) {
	b, err := os.ReadFile("conversation_loop.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(b)
	compIdx := strings.Index(s, "a.injectSubagentCompletions()")
	mboxIdx := strings.Index(s, "a.injectMailboxMessages()")
	if compIdx < 0 || mboxIdx < 0 {
		t.Fatal("conversation loop lost an injection site (completions and/or mailbox)")
	}
	if mboxIdx < compIdx {
		t.Fatal("mailbox drain must run inside the per-iteration injection block")
	}
}
