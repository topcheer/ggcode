package agent

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

type shrinkCM struct {
	*ctxpkg.Manager
	shrunk bool
}

func (s *shrinkCM) shrink() { s.shrunk = true }

func (s *shrinkCM) ApplyCompactResult(snap ctxpkg.CompactSnapshot, r ctxpkg.CompactResult) (bool, int) {
	if s.shrunk {

		s.Manager.Clear()
		s.Manager.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post-rewind"}}})
	}
	return s.Manager.ApplyCompactResult(snap, r)
}

func (s *shrinkCM) TokenCount() int           { return s.Manager.AutoCompactThreshold() + 1000 }
func (s *shrinkCM) AutoCompactThreshold() int { return s.Manager.AutoCompactThreshold() }

func TestIssue651_ManagerRejectsLiveShrunkSnapshot(t *testing.T) {
	cm := ctxpkg.NewManager(100000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	for i := 0; i < 3; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q"}}})
	}
	snapshot := cm.CompactSnapshot()

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

func TestIssue651_LiveShrunkDiscardRefundsWithRealManager(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)
	scm := &shrinkCM{Manager: base}
	a.SetContextManager(scm)

	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	base.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	snapshot := base.CompactSnapshot()
	scm.shrink()

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
