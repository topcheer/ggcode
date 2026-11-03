package agent

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

type benignCM struct {
	*ctxpkg.Manager
	trimmed bool
}

func (b *benignCM) ApplyCompactResult(snap ctxpkg.CompactSnapshot, r ctxpkg.CompactResult) (bool, int) {
	if b.trimmed {

		b.Manager.RemoveLastAssistantGroup()
	}
	return b.Manager.ApplyCompactResult(snap, r)
}

func TestIssue663_BenignTrimKeepsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)

	base.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	base.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	bcm := &benignCM{Manager: base}

	snap := bcm.CompactSnapshot()

	_ = bcm.RemoveLastAssistantGroup()

	if rr := bcm.LastCompactRejectReason(); rr != ctxpkg.CompactRejectNone {
		t.Fatalf("no rejection has happened yet, expected none, got %v", rr)
	}

	applied, _ := bcm.ApplyCompactResult(snap, ctxpkg.CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	})
	if applied {
		t.Fatal("live-shrunk snapshot (benign trim) must still be rejected (#651 semantics preserved)")
	}
	if rr := bcm.LastCompactRejectReason(); rr != ctxpkg.CompactRejectBenignTrim {
		t.Fatalf("benign internal cleanup must classify as benign-trim (#663), got %v", rr)
	}
}

func TestIssue663_UserResetStillRefundsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)

	base.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	for i := 0; i < 3; i++ {
		base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q"}}})
	}
	snap := base.CompactSnapshot()

	base.Clear()
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post-clear"}}})

	applied, _ := base.ApplyCompactResult(snap, ctxpkg.CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	})
	if applied {
		t.Fatal("user-reset (clear) during window must be rejected (#651)")
	}
	if rr := base.LastCompactRejectReason(); rr != ctxpkg.CompactRejectUserReset {
		t.Fatalf("user-driven clear must classify as user-reset (#663), got %v", rr)
	}

	a.SetContextManager(base)
	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: base.AutoCompactThreshold() + 1000,
		snapshot: snap,
		result: ctxpkg.CompactResult{
			Changed:  true,
			Messages: []provider.Message{{Role: "system"}},
		},
	}
	close(pc.done)
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (live shrunk by user reset)")
	}
	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if !cd.IsZero() {
		t.Fatalf("user-reset discard must still refund cooldown (#612/#651 intact), remaining=%s", time.Until(cd).Round(time.Second))
	}
}

func TestIssue663_BenignTrimDiscardKeepsCooldownAgentPath(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)

	base.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	base.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snap := base.CompactSnapshot()

	_ = base.RemoveLastAssistantGroup()

	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: base.AutoCompactThreshold() + 1000,
		snapshot: snap,
		result: ctxpkg.CompactResult{
			Changed:  true,
			Messages: []provider.Message{{Role: "system"}},
		},
	}
	close(pc.done)
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (benign trim shrank live list)")
	}
	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if cd.IsZero() {
		t.Fatal("benign-trim discard must NOT refund cooldown (#663) — refund would reschedule a redundant full-context summarization every turn")
	}
}

func TestIssue663_SnapshotResetsAttribution(t *testing.T) {
	cm := ctxpkg.NewManager(100000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	_ = cm.RemoveLastAssistantGroup()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a2"}}})
	snap := cm.CompactSnapshot()

	cm.Clear()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "after"}}})

	applied, _ := cm.ApplyCompactResult(snap, ctxpkg.CompactResult{
		Messages:   []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
		TokenCount: 5,
		Changed:    true,
	})
	if applied {
		t.Fatal("user reset during window must be rejected")
	}
	if rr := cm.LastCompactRejectReason(); rr != ctxpkg.CompactRejectUserReset {
		t.Fatalf("stale benign marker from before the snapshot must not mask a user reset (#663), got %v", rr)
	}
}
