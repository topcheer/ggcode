package agent

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func injectDonePrecompact(t *testing.T, a *Agent) {
	t.Helper()
	pc := &precompactState{
		done:     make(chan struct{}),
		snapshot: ctxpkg.CompactSnapshot{},
		result:   ctxpkg.CompactResult{Changed: false},
	}
	close(pc.done)
	a.mu.Lock()
	a.precompact = pc
	a.mu.Unlock()
}

func TestIssue612_DiscardRefundsCooldownWhenOverThreshold(t *testing.T) {
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
		t.Fatalf("cooldown must be refunded after live-shrunk discard while over threshold; remaining=%s", time.Until(cd).Round(time.Second))
	}
}

func TestIssue612_DiscardKeepsCooldownWhenUnderThreshold(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	a.ContextManager().SetContextWindow(50000)
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("hi")}})

	future := time.Now().Add(2 * time.Minute)
	a.mu.Lock()
	a.precompactCooldownUntil = future
	a.mu.Unlock()

	injectDonePrecompact(t, a)
	if applied := a.consumeReadyPreCompact(nil); applied {
		t.Fatal("expected discard (Changed=false result)")
	}

	a.mu.RLock()
	cd := a.precompactCooldownUntil
	a.mu.RUnlock()
	if cd.IsZero() || !cd.Equal(future) {
		t.Fatalf("cooldown must be preserved when under threshold, got %v (want %v)", cd, future)
	}
}
