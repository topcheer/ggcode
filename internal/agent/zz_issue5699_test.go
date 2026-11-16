package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

// #5699: following turns must carry the deferred-answer state to the model —
// the "N pending MCP interactions await answers" notice plus one-shot
// digests of answered interactions.

type stub5699Status struct {
	count    int
	lines    []string
	answered []string
}

func (s *stub5699Status) PendingMCPAnswerCount() int { return s.count }
func (s *stub5699Status) PendingMCPAnswerLines() []string {
	return s.lines
}
func (s *stub5699Status) TakeAnsweredMCPAnswerLines() []string {
	out := s.answered
	s.answered = nil
	return out
}

func new5699Agent(t *testing.T, src MCPInteractionStatus) *Agent {
	t.Helper()
	a := &Agent{contextManager: ctxpkg.NewManager(200000)}
	if src != nil {
		a.SetMCPInteractionStatus(src)
	}
	return a
}

func last5699MessageText(t *testing.T, a *Agent) string {
	t.Helper()
	msgs := a.contextManager.Messages()
	if len(msgs) == 0 {
		t.Fatal("no messages")
	}
	last := msgs[len(msgs)-1]
	var b strings.Builder
	for _, blk := range last.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// TestIssue5699ModelNoticeInFollowingTurns asserts the acceptance criterion:
// while N > 0 pending interactions exist, every following turn carries the
// "N pending MCP interactions await answers" notice.
func TestIssue5699ModelNoticeInFollowingTurns(t *testing.T) {
	src := &stub5699Status{
		count: 2,
		lines: []string{
			"pa-1 on server 'deploy' (tool deploy, expires in 30m): Which city?",
			"pa-2 on server 'deploy' (tool deploy, expires in 29m): Which region?",
		},
	}
	a := new5699Agent(t, src)

	a.maybeInjectPendingMCPAnswers()
	text := last5699MessageText(t, a)
	if !strings.Contains(text, "2 pending MCP interactions await answers") {
		t.Fatalf("notice missing N-pending line: %s", text)
	}
	if !strings.Contains(text, "pa-1") || !strings.Contains(text, "pa-2") {
		t.Fatalf("notice must list pending entries: %s", text)
	}
	if !strings.Contains(text, "/mcp answer") {
		t.Fatalf("notice must teach the answer command: %s", text)
	}

	// The notice repeats on the NEXT turn while still pending (following
	// turns, not just once).
	a.maybeInjectPendingMCPAnswers()
	if !strings.Contains(last5699MessageText(t, a), "2 pending MCP interactions await answers") {
		t.Fatal("notice must persist in following turns while pending")
	}

	// Once answered (count drops to zero), the pending notice disappears
	// (no new message is injected at all).
	src.count = 0
	src.lines = nil
	before := len(a.contextManager.Messages())
	a.maybeInjectPendingMCPAnswers()
	if after := len(a.contextManager.Messages()); after != before {
		t.Fatalf("notice must disappear once nothing is pending; messages %d -> %d", before, after)
	}
}

// TestIssue5699AnsweredDigestOneShot asserts answered interactions surface
// exactly once: the turn after the answer carries the resumed result digest,
// later turns do not repeat it.
func TestIssue5699AnsweredDigestOneShot(t *testing.T) {
	src := &stub5699Status{
		answered: []string{"[mcp] The user answered deferred MCP interaction pa-1 (server 'deploy', tool deploy): resumed result: done"},
	}
	a := new5699Agent(t, src)

	a.maybeInjectPendingMCPAnswers()
	text := last5699MessageText(t, a)
	if !strings.Contains(text, "answered deferred MCP interaction pa-1") || !strings.Contains(text, "resumed result: done") {
		t.Fatalf("answered digest missing: %s", text)
	}

	before := len(a.contextManager.Messages())
	a.maybeInjectPendingMCPAnswers()
	if after := len(a.contextManager.Messages()); after != before {
		t.Fatal("answered digest must be one-shot; a later turn re-injected it")
	}
}

// TestIssue5699SingularWording pins the N==1 wording.
func TestIssue5699SingularWording(t *testing.T) {
	src := &stub5699Status{count: 1, lines: []string{"pa-9 on server 'x' (tool y, expires in 30m)"}}
	a := new5699Agent(t, src)
	a.maybeInjectPendingMCPAnswers()
	if !strings.Contains(last5699MessageText(t, a), "1 pending MCP interaction awaits an answer") {
		t.Fatal("singular wording missing")
	}
}

// TestIssue5699NilStatusNoop asserts headless sessions (no MCP wiring) are
// untouched.
func TestIssue5699NilStatusNoop(t *testing.T) {
	a := new5699Agent(t, nil)
	a.maybeInjectPendingMCPAnswers()
	if n := len(a.contextManager.Messages()); n != 0 {
		t.Fatalf("nil status must inject nothing, got %d messages", n)
	}
}
