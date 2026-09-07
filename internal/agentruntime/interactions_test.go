package agentruntime

import (
	"context"
	"runtime"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestAwaitApprovalHonorsBufferedDecisionOnCtxDone(t *testing.T) {
	// Regression for #1482: when ResolveApproval has buffered the decision
	// (and already acknowledged it to the UI) and ctx.Done fires in the same
	// select window, the old code randomly returned Deny - discarding an
	// approval the user saw as granted. Run the race 20 times so the old
	// behavior cannot pass by luck.
	for i := 0; i < 20; i++ {
		b := NewInteractionBroker()
		ctx, cancel := context.WithCancel(context.Background())
		got := make(chan permission.Decision, 1)
		go func() {
			got <- b.AwaitApproval(ctx, ApprovalRequest{ID: "t1"})
		}()
		for {
			b.mu.Lock()
			n := len(b.approvals)
			b.mu.Unlock()
			if n == 1 {
				break
			}
			runtime.Gosched()
		}
		if _, ok := b.ResolveApproval("t1", permission.Allow); !ok {
			cancel()
			t.Fatal("waiter not found - registration race in test setup")
		}
		cancel() // both select cases ready; the drain must win
		if d := <-got; d != permission.Allow {
			t.Fatalf("iter %d: buffered Allow discarded on ctx.Done (got %v)", i, d)
		}
	}
}

// Regression for #1657: with 2+ pending approvals (parallel tool calls),
// FirstPendingApproval used "for range map return first" - Go map iteration
// order is randomized, so a text "y" landed on an arbitrary request. Run
// 20 times so random iteration order cannot pass by luck: the
// earliest-registered approval must always win.
func TestFirstPendingApprovalDeterministicOrder(t *testing.T) {
	for i := 0; i < 20; i++ {
		b := NewInteractionBroker()
		ctx, cancel := context.WithCancel(context.Background())
		go b.AwaitApproval(ctx, ApprovalRequest{ID: "first"})
		waitForBrokerMapLen(t, b, 1)
		go b.AwaitApproval(ctx, ApprovalRequest{ID: "second"})
		waitForBrokerMapLen(t, b, 2)

		req, ok := b.FirstPendingApproval()
		if !ok {
			t.Fatalf("iteration %d: expected a pending approval", i)
		}
		if req.ID != "first" {
			t.Fatalf("iteration %d: FirstPendingApproval returned %q, want earliest-registered %q", i, req.ID, "first")
		}

		// After resolving the first, the next First call must return the
		// second one (not a random one).
		if _, ok := b.ResolveApproval("first", permission.Deny); !ok {
			t.Fatalf("iteration %d: first waiter not found", i)
		}
		req, ok = b.FirstPendingApproval()
		if !ok || req.ID != "second" {
			t.Fatalf("iteration %d: after resolving first, got (%q, %v), want second", i, req.ID, ok)
		}
		cancel()
		b.CancelAll()
	}
}

func TestFirstPendingAskUserDeterministicOrder(t *testing.T) {
	for i := 0; i < 20; i++ {
		b := NewInteractionBroker()
		ctx, cancel := context.WithCancel(context.Background())
		go b.AwaitAskUser(ctx, AskUserRequest{ID: "a1"})
		waitForAskMapLen(t, b, 1)
		go b.AwaitAskUser(ctx, AskUserRequest{ID: "a2"})
		waitForAskMapLen(t, b, 2)

		req, ok := b.FirstPendingAskUser()
		if !ok || req.ID != "a1" {
			t.Fatalf("iteration %d: FirstPendingAskUser got (%q, %v), want earliest a1", i, req.ID, ok)
		}
		cancel()
		b.CancelAll()
	}
}

func waitForBrokerMapLen(t *testing.T, b *InteractionBroker, want int) {
	t.Helper()
	for {
		b.mu.Lock()
		n := len(b.approvals)
		b.mu.Unlock()
		if n == want {
			return
		}
		runtime.Gosched()
	}
}

func waitForAskMapLen(t *testing.T, b *InteractionBroker, want int) {
	t.Helper()
	for {
		b.mu.Lock()
		n := len(b.askUsers)
		b.mu.Unlock()
		if n == want {
			return
		}
		runtime.Gosched()
	}
}
