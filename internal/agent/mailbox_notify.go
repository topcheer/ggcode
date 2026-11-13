package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

// MailboxFeed drains pending mailbox messages for a running agent. It must
// be non-blocking (drain the channel with select/default) and consume-once:
// returned messages count as delivered and are never returned again.
type MailboxFeed = func() []subagent.AgentMessage

// mailboxMessageRunes bounds one injected message body; send_message
// content is meant for steering, not bulk transfer.
const mailboxMessageRunes = 2000

// SetMailboxFeed installs the drain polled between conversation-loop
// iterations. nil (the default) disables mailbox consumption - agents
// spawned without a manager mailbox are unaffected.
func (a *Agent) SetMailboxFeed(fn MailboxFeed) {
	a.mu.Lock()
	a.mailboxFeed = fn
	a.mu.Unlock()
}

// injectMailboxMessages polls the mailbox feed and, when messages are
// pending, injects them as ONE user-role block so the agent sees them on
// its next LLM iteration. This is the consumption half of the send_message
// mailbox channel, symmetric with injectSubagentCompletions (which carries
// sub-agent results back the other way). Consume-once: no seen-set is
// needed because the feed drains the channel rather than snapshotting it.
func (a *Agent) injectMailboxMessages() bool {
	a.mu.RLock()
	feed := a.mailboxFeed
	a.mu.RUnlock()
	if feed == nil {
		return false
	}
	msgs := feed()
	if len(msgs) == 0 {
		return false
	}

	var b strings.Builder
	b.WriteString("Message(s) arrived for you while you were working:\n")
	for _, m := range msgs {
		line := fmt.Sprintf("- from %s", m.From)
		if s := strings.TrimSpace(m.Summary); s != "" {
			line += " (" + s + ")"
		}
		line += ": " + truncateRunes(strings.TrimSpace(m.Message), mailboxMessageRunes)
		b.WriteString(line + "\n")
	}
	b.WriteString("Treat these as input from the sender and incorporate them into your current task. Do not spawn further agents.")

	debug.Log("agent", "injecting mailbox message(s) (n=%d)", len(msgs))
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: b.String(),
		}},
	})
	return true
}
