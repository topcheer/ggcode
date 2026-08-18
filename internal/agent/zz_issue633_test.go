package agent

// #633: the #612 refund did not distinguish discard reasons. no-change/empty
// are real summarization failures — refunding their cooldown made
// maybeAutoCompact reschedule a full-context LLM summarization every turn
// (back-to-back, no backoff) that failed the same way each time. Only the
// "live messages shrunk below snapshot size" discard (summarization
// succeeded, live context moved on) keeps the refund.

import (
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// discardCM is a ContextManager whose snapshot application always fails the
// apply (returns false) while the manager itself reports tokens still above
// the auto-compact threshold. It is used to drive the discard branch with a
// result that IS a successful summarization (Changed=true, non-empty).
type discardCM struct {
	*ctxpkg.Manager // embed by pointer: Manager contains a sync.Mutex; value embedding copies the lock (vet copylocks)
	threshold       int
	tokens          int
}

func (d *discardCM) ApplyCompactResult(s ctxpkg.CompactSnapshot, r ctxpkg.CompactResult) (bool, int) {
	return false, d.tokens
}

func (d *discardCM) AutoCompactThreshold() int { return d.threshold }
func (d *discardCM) TokenCount() int           { return d.tokens }
func (d *discardCM) Messages() []provider.Message {
	return nil // live message list shorter than snapshot -> "shrunk" reason
}

// A discard because the live context moved on (summarization succeeded) must
// still refund the cooldown when tokens are over the threshold (#612 intact).
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
		startTok: 500, // #651: the refund gate reads tokens at SCHEDULE time
		snapshot: ctxpkg.CompactSnapshot{OrigLen: 3, Messages: make([]provider.Message, 3)},
		result:   ctxpkg.CompactResult{Changed: true, Messages: []provider.Message{{Role: "system"}}}, // successful summary, non-empty
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

// A discard because the summarization produced no change must KEEP the
// cooldown — refunding it caused back-to-back full-context LLM calls.
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
		result:   ctxpkg.CompactResult{Changed: false}, // no-change summarization failure
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

// A discard because the summarization produced an empty result must KEEP the
// cooldown as well.
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
		result:   ctxpkg.CompactResult{Changed: true, Messages: nil}, // empty result failure
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
