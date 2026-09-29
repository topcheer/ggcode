package agent

import (
	"sync"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// zz_issue2905_test.go - regression probes for #2905: the fallback
// checkpoint must (a) not fire on EVERY Run once past the threshold (the
// old equality dedup never re-matched monotonically growing counts) and
// (b) write a REAL last message ID instead of "" (an empty-lastMsgID record
// becoming the latest checkpoint degraded resume positioning).

// stubCompactCM is a minimal ContextManager for checkpoint-path tests. It
// embeds the interface so unimplemented methods panic loudly if reached.
type stubCompactCM struct {
	ctxpkg.ContextManager
	msgs    []provider.Message
	summary string
}

func (s *stubCompactCM) Messages() []provider.Message { return s.msgs }
func (s *stubCompactCM) SummaryMsgID() string         { return s.summary }
func (s *stubCompactCM) TokenCount() int              { return 1234 }
func (s *stubCompactCM) Add(m provider.Message)       {}

func newFallbackTestAgent(t *testing.T, nMsgs int) (*Agent, *[]string) {
	t.Helper()
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "test", 5)
	t.Cleanup(a.Close)

	msgs := make([]provider.Message, nMsgs)
	for i := range msgs {
		msgs[i] = provider.Message{ID: "msg-2905", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "x"}}}
	}
	msgs[nMsgs-1].ID = "last-2905"
	a.SetContextManager(&stubCompactCM{msgs: msgs, summary: "summary-2905"})

	var mu sync.Mutex
	var lastIDs []string
	a.mu.Lock()
	a.onCheckpoint = func(summaryMsgID, lastMsgID string, tokenCount int) {
		mu.Lock()
		lastIDs = append(lastIDs, lastMsgID)
		mu.Unlock()
	}
	a.mu.Unlock()
	return a, &lastIDs
}

func TestIssue2905FallbackWritesRealLastMsgID(t *testing.T) {
	a, lastIDs := newFallbackTestAgent(t, 600)
	a.maybeFallbackCheckpoint()
	if len(*lastIDs) != 1 {
		t.Fatalf("expected one checkpoint write, got %d", len(*lastIDs))
	}
	if got := (*lastIDs)[0]; got != "last-2905" {
		t.Fatalf("#2905: fallback checkpoint lastMsgID must be the last message ID, got %q", got)
	}
}

func TestIssue2905FallbackSpamSuppressed(t *testing.T) {
	a, lastIDs := newFallbackTestAgent(t, 600)
	a.maybeFallbackCheckpoint() // first write at count 600

	// Small growth (below the interval): must NOT fire again.
	cm := a.contextManager.(*stubCompactCM)
	cm.msgs = append(cm.msgs, provider.Message{ID: "m-601", Role: "user"})
	a.maybeFallbackCheckpoint()
	if len(*lastIDs) != 1 {
		t.Fatalf("#2905: fallback fired again after only %d new messages (spam not suppressed): %d writes", 1, len(*lastIDs))
	}

	// Real growth (>= interval): fires again, still with a real anchor.
	for i := 0; i < fallbackCheckpointInterval; i++ {
		cm.msgs = append(cm.msgs, provider.Message{ID: "bulk", Role: "user"})
	}
	cm.msgs[len(cm.msgs)-1].ID = "last-after-growth"
	a.maybeFallbackCheckpoint()
	if len(*lastIDs) != 2 {
		t.Fatalf("expected second checkpoint after interval growth, got %d writes", len(*lastIDs))
	}
	if got := (*lastIDs)[1]; got != "last-after-growth" {
		t.Fatalf("second write must anchor at new last message, got %q", got)
	}
}
