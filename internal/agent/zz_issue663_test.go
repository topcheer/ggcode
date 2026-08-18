package agent

// #663: #651's liveShrunk refund was attributed by exclusion — ANY rejection
// other than no-change/empty was treated as "live context moved on" and
// refunded the cooldown. But benign internal cleanup (retry truncation via
// RemoveLastAssistantGroup, orphan tool-result removal via ReconcileToolCalls)
// also shrinks the live message list with NO semantic loss. Refunding on those
// made maybeAutoCompact reschedule a redundant full-context LLM summarization
// every turn (retry storms amplified the waste). Fix: Manager.ApplyCompactResult
// now records a structured reject reason (user-reset vs benign-trim) and the
// agent refunds ONLY on user-driven resets.

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// benignCM simulates a benign internal cleanup (retry truncation) during the
// compaction window: the live list loses tail messages via
// RemoveLastAssistantGroup — no semantic loss, no /clear.
type benignCM struct {
	*ctxpkg.Manager
	trimmed bool
}

func (b *benignCM) ApplyCompactResult(snap ctxpkg.CompactSnapshot, r ctxpkg.CompactResult) (bool, int) {
	if b.trimmed {
		// Simulate the retry truncation that happened during the window:
		// drop the last assistant group like the /regenerate path does.
		b.Manager.RemoveLastAssistantGroup()
	}
	return b.Manager.ApplyCompactResult(snap, r)
}

// TestIssue663_BenignTrimKeepsCooldown: a discard caused by benign internal
// cleanup (retry truncation) must NOT refund the cooldown — the live context
// is still essentially the one that warranted compaction, so a refund would
// reschedule a redundant full-context summarization.
func TestIssue663_BenignTrimKeepsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)

	// Build a live context whose tail can be benignly truncated.
	base.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	base.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	bcm := &benignCM{Manager: base}
	// Snapshot BEFORE the benign trim so the live list will be shorter.
	snap := bcm.CompactSnapshot()
	// Benign trim after the snapshot: RemoveLastAssistantGroup marks
	// benignRemoval; the next ApplyCompactResult must reject with benign-trim.
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

// TestIssue663_UserResetStillRefundsCooldown: /clear (a user-driven reset)
// during the compaction window must still reject AND still refund the
// cooldown — #612/#651 behavior preserved for genuine resets.
func TestIssue663_UserResetStillRefundsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)

	base.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	for i := 0; i < 3; i++ {
		base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q"}}})
	}
	snap := base.CompactSnapshot()
	// User-driven clear during the window.
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

	// End-to-end through the agent loop: a user-reset discard refunds, a
	// benign-trim discard does not. Drive consumeReadyPreCompact with a
	// manager that reports tokens over threshold at schedule time (#651 gate).
	// NOTE: SetContextManager must run BEFORE arming pc — it calls
	// CancelPreCompact() and would clear the slot we are about to set
	// (production wires the manager long before scheduling a precompact).
	a.SetContextManager(base)
	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: base.AutoCompactThreshold() + 1000, // over threshold — mirrors schedule-time (#651)
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

// TestIssue663_BenignTrimDiscardKeepsCooldownAgentPath: end-to-end through
// consumeReadyPreCompact — a benign-trim discard must leave the cooldown in
// place (no refund), unlike the pre-#663 exclusion-based attribution which
// refunded on every non-no-change/empty rejection.
func TestIssue663_BenignTrimDiscardKeepsCooldownAgentPath(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)

	base.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	base.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	base.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	snap := base.CompactSnapshot()
	// Benign retry truncation during the window.
	_ = base.RemoveLastAssistantGroup()

	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: base.AutoCompactThreshold() + 1000, // over threshold — pre-#663 this refunded (#651 gate)
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

// TestIssue663_SnapshotResetsAttribution: taking a new compact snapshot opens
// a fresh attribution window — a benign removal from BEFORE the snapshot must
// not poison the next window's classification.
func TestIssue663_SnapshotResetsAttribution(t *testing.T) {
	cm := ctxpkg.NewManager(100000)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a1"}}})

	// Benign trim, then a fresh snapshot — the marker must be cleared.
	_ = cm.RemoveLastAssistantGroup()
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a2"}}})
	snap := cm.CompactSnapshot()

	// Now a user clear during the NEW window.
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
