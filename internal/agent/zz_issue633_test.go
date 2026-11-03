package agent

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

type discardCM struct {
	*ctxpkg.Manager
	threshold int
	tokens    int
}

func (d *discardCM) ApplyCompactResult(s ctxpkg.CompactSnapshot, r ctxpkg.CompactResult) (bool, int) {
	return false, d.tokens
}

func (d *discardCM) AutoCompactThreshold() int { return d.threshold }
func (d *discardCM) TokenCount() int           { return d.tokens }
func (d *discardCM) Messages() []provider.Message {
	return nil
}

func TestIssue633_LiveShrunkDiscardStillRefundsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)
	dm := &discardCM{Manager: base, threshold: 100, tokens: 500}
	a.SetContextManager(dm)

	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: 500,
		snapshot: ctxpkg.CompactSnapshot{OrigLen: 3, Messages: make([]provider.Message, 3)},
		result:   ctxpkg.CompactResult{Changed: true, Messages: []provider.Message{{Role: "system"}}},
	}
	close(pc.done)
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (apply rejected)")
	}
	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if !cd.IsZero() {
		t.Fatalf("live-shrunk discard must refund cooldown (#612), remaining=%s", time.Until(cd).Round(time.Second))
	}
}

func TestIssue633_NoChangeDiscardKeepsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)
	dm := &discardCM{Manager: base, threshold: 100, tokens: 500}
	a.SetContextManager(dm)

	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		snapshot: ctxpkg.CompactSnapshot{},
		result:   ctxpkg.CompactResult{Changed: false},
	}
	close(pc.done)
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (Changed=false)")
	}
	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if cd.IsZero() || !cd.Equal(future) {
		t.Fatalf("no-change discard must keep cooldown (#633), got %v want %v", cd, future)
	}
}

func TestIssue633_EmptyResultDiscardKeepsCooldown(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	base := a.ContextManager().(*ctxpkg.Manager)
	dm := &discardCM{Manager: base, threshold: 100, tokens: 500}
	a.SetContextManager(dm)

	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	pc := &precompactState{
		done:     make(chan struct{}),
		snapshot: ctxpkg.CompactSnapshot{OrigLen: 3, Messages: make([]provider.Message, 3)},
		result:   ctxpkg.CompactResult{Changed: true, Messages: nil},
	}
	close(pc.done)
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (empty result)")
	}
	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if cd.IsZero() || !cd.Equal(future) {
		t.Fatalf("empty-result discard must keep cooldown (#633), got %v want %v", cd, future)
	}
}
