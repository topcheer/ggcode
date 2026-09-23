package agent

import (
	"sync"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #2687: on a successful auto-precompact consumption (applied=true),
// on_compaction fired TWICE - the sa-159 auto-trigger call plus a legacy
// trailing call with an empty CompactTrigger that sa-159 failed to delete.
// Every counting/notification hook double-counted and the second event fell
// outside the documented auto/reactive/manual trigger enum. The invariant:
// exactly ONE fire, with trigger="auto".
func TestIssue2687_SingleOnCompactionFireOnApplied(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	defer a.Close()

	// Seed the live context so the compact result applies (not discarded).
	for i := 0; i < 3; i++ {
		a.ContextManager().Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "m"}}})
	}

	var mu sync.Mutex
	var fires []hooks.HookEnv
	prev := sa159RunCompactionHooks
	sa159RunCompactionHooks = func(cfg hooks.HookConfig, env hooks.HookEnv) {
		mu.Lock()
		fires = append(fires, env)
		mu.Unlock()
	}
	t.Cleanup(func() { sa159RunCompactionHooks = prev })

	pc := &precompactState{
		done:     make(chan struct{}),
		startTok: 500,
		snapshot: ctxpkg.CompactSnapshot{OrigLen: 3, Messages: make([]provider.Message, 3)},
		result:   ctxpkg.CompactResult{Changed: true, Messages: []provider.Message{{Role: "system"}}},
	}
	close(pc.done)
	a.mu.Lock()
	a.precompact = pc
	a.mu.Unlock()

	if applied := a.consumeReadyPreCompact(nil); !applied {
		t.Fatal("expected the completed precompact to apply")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fires) != 1 {
		t.Fatalf("on_compaction fired %d times on successful auto-precompact, want exactly 1 (#2687 duplicate)", len(fires))
	}
	if fires[0].CompactTrigger != "auto" {
		t.Fatalf("CompactTrigger = %q, want %q (documented enum auto/reactive/manual)", fires[0].CompactTrigger, "auto")
	}
}
