package agent

// #612: consumeReadyPreCompact's discard branch did not refund the 2-minute
// precompact cooldown set at schedule time. When tokens are still above the
// auto-compact threshold, the stale cooldown blocks re-scheduling and the
// only escape left is destructive truncation once tokens cross promptBudget.

import (
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// injectDonePrecompact installs a completed (done) precompact state whose
// result will be discarded by ApplyCompactResult (Changed=false).
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

// Discard while tokens are still over the auto-compact threshold must refund
// the cooldown so the next loop pass can schedule a fresh precompact.
func TestIssue612_DiscardRefundsCooldownWhenOverThreshold(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	a.ContextManager().SetContextWindow(80)
	for i := 0; i < 6; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("old context ", 8))}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock(strings.Repeat("reply ", 8))}})
	}
	if tokens := a.ContextManager().TokenCount(); tokens < a.ContextManager().AutoCompactThreshold() {
		t.Fatalf("setup: tokens=%d must exceed threshold=%d", tokens, a.ContextManager().AutoCompactThreshold())
	}

	// Simulate the cooldown set by maybeAutoCompact at schedule time.
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
	if !cd.IsZero() {
		t.Fatalf("cooldown must be refunded after discard while over threshold; remaining=%s", time.Until(cd).Round(time.Second))
	}
}

// Discard while tokens have fallen below the threshold must KEEP the cooldown
// (the cooldown-on-failure design stays intact when there is no pressure).
func TestIssue612_DiscardKeepsCooldownWhenUnderThreshold(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()
	a.ContextManager().SetContextWindow(50000) // threshold far above tokens
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
