package agent

// #651: Manager.ApplyCompactResult had NO live-shrunk rejection path in
// production (its only `return false` was !Changed || empty), so the #633
// liveShrunk refund branch in consumeReadyPreCompact was mock-only dead
// code. The context package now rejects snapshots whose live message list
// shrank below the snapshot (messages removed during the compaction window —
// /clear, checkpoint rewind); these tests pin that the refund is reachable
// with the REAL *ctxpkg.Manager.

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// shrinkCM wraps the real Manager; on demand it rewinds the live message
// list below the snapshot size (checkpoint-rewind style), so the production
// ApplyCompactResult takes the live-shrunk rejection path.
type shrinkCM struct {
	*ctxpkg.Manager
	shrunk bool
}

func (s *shrinkCM) shrink() { s.shrunk = true }

func (s *shrinkCM) ApplyCompactResult(snap ctxpkg.CompactSnapshot, r ctxpkg.CompactResult) (bool, int) {
	if s.shrunk {
		// Simulate the rewind that happened during the compaction window:
		// live context now holds fewer messages than the snapshot captured.
		s.Manager.Clear()
		s.Manager.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post-rewind"}}})
	}
	return s.Manager.ApplyCompactResult(snap, r)
}

// The #612/#633/#651 refund gate reads tokens at SCHEDULE time (pc.startTok)
// against AutoCompactThreshold(). The physical fixture holds three tiny
// messages in a 128k window; the wrapper keeps the schedule-time view
// consistent: threshold passes through from the real Manager (115200) and
// the token count reflects what maybeAutoCompact saw when scheduling —
// the reject path under test is the REAL Manager's ApplyCompactResult above.
func (s *shrinkCM) TokenCount() int           { return s.Manager.AutoCompactThreshold() + 1000 }
func (s *shrinkCM) AutoCompactThreshold() int { return s.Manager.AutoCompactThreshold() }

// The real Manager must reject a result whose live list shrank below the
// snapshot — this is the production path that feeds the agent-side refund.
func TestIssue651_ManagerRejectsLiveShrunkSnapshot(t *testing.T) {
	cm := ctxpkg.NewManager(100000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	for i := 0; i < 3; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q"}}})
	}
	snapshot := cm.CompactSnapshot()

	// Messages removed during the compaction window.
	cm.Clear()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "only one message"}}})

	result := ctxpkg.CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	}
	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if applied {
		t.Fatal("live-shrunk snapshot (messages removed during compaction) must be rejected (#651)")
	}
}

// Messages APPENDED during the window are not a shrink — apply must succeed
// (regression guard for the rejection's size bound).
func TestIssue651_AppendOnlyStillApplies(t *testing.T) {
	cm := ctxpkg.NewManager(100000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	snapshot := cm.CompactSnapshot()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2 arrived during window"}}})

	result := ctxpkg.CompactResult{
		Messages:   []provider.Message{{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]"}}}},
		TokenCount: 5,
		Changed:    true,
	}
	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("append-only window must still apply")
	}
	found := false
	for _, msg := range cm.Messages() {
		for _, b := range msg.Content {
			if b.Text == "q2 arrived during window" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("appended message lost")
	}
}

// End-to-end: a successful summarization discarded because the live context
// was rewound during the window must refund the precompact cooldown — using
// the REAL context Manager on the agent (no mock ApplyCompactResult).
func TestIssue651_LiveShrunkDiscardRefundsWithRealManager(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)
	scm := &shrinkCM{Manager: base}
	a.SetContextManager(scm)

	// Fill the live context, snapshot it, then rewind below the snapshot —
	// all through the wrapper so production ApplyCompactResult sees the shrink.
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	base.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	snapshot := base.CompactSnapshot()
	scm.shrink()

	// Cooldown set by maybeAutoCompact at schedule time. #651: the refund
	// gate uses pc.startTok (tokens at SCHEDULE time) — compaction is only
	// scheduled when tokens are over threshold, so mirror that here.
	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: base.AutoCompactThreshold() + 1000,
		snapshot: snapshot,
		result: ctxpkg.CompactResult{
			Changed:  true,
			Messages: []provider.Message{{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]"}}}},
		},
	}
	close(pc.done)
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (live-shrunk rejection)")
	}
	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if !cd.IsZero() {
		t.Fatalf("live-shrunk discard via REAL Manager must refund cooldown (#651), remaining=%s", time.Until(cd).Round(time.Second))
	}
}
