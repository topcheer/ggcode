package agentruntime

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
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

// #3370: with NO buffered decision, a fired ctx.Done must return the
// matching NON-DECISION outcome (timeout/cancelled), never a bare Deny -
// downstream audit/approval-memory/throttle must not blame the user.
func TestAwaitApprovalCtxDoneReturnsNonDecision(t *testing.T) {
	t.Run("deadline-expired", func(t *testing.T) {
		b := NewInteractionBroker()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		d := b.AwaitApproval(ctx, ApprovalRequest{ID: "t1"})
		if d != permission.Timeout {
			t.Fatalf("deadline ctx.Done -> %v, want Timeout", d)
		}
		if !d.IsNonDecision() {
			t.Fatal("Timeout must be classified as a non-decision")
		}
	})
	t.Run("run-cancelled", func(t *testing.T) {
		b := NewInteractionBroker()
		got := make(chan permission.Decision, 1)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { got <- b.AwaitApproval(ctx, ApprovalRequest{ID: "t2"}) }()
		for {
			b.mu.Lock()
			n := len(b.approvals)
			b.mu.Unlock()
			if n == 1 {
				break
			}
			runtime.Gosched()
		}
		cancel()
		if d := <-got; d != permission.Cancelled {
			t.Fatalf("cancelled ctx.Done -> %v, want Cancelled", d)
		}
	})
}

// Regression for #4151: desktop approval handlers with no tunnel broker used
// to pass requestID "" for every request. Two concurrent approvals then
// collided on the same map key and the second registration silently
// overwrote the first waiter - the user's Allow on dialog A resolved
// waiter B, B's actual decision vanished, and waiter A hung until its
// 15-minute timeout. The broker must re-key the later waiter so both stay
// independently resolvable.
func TestAwaitApprovalSameIDDoesNotOverwriteWaiter(t *testing.T) {
	b := NewInteractionBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gotA := make(chan permission.Decision, 1)
	gotB := make(chan permission.Decision, 1)
	go func() { gotA <- b.AwaitApproval(ctx, ApprovalRequest{ID: "", ToolName: "toolA"}) }()
	waitForBrokerMapLen(t, b, 1)
	go func() { gotB <- b.AwaitApproval(ctx, ApprovalRequest{ID: "", ToolName: "toolB"}) }()
	waitForBrokerMapLen(t, b, 2)

	// The first waiter keeps the original key; resolving it must reach A.
	if _, ok := b.ResolveApproval("", permission.Allow); !ok {
		t.Fatal("first waiter (original empty ID) is not resolvable")
	}
	// The second waiter must have been re-keyed, not overwritten/lost.
	pending := b.PendingApprovals()
	if len(pending) != 1 {
		t.Fatalf("after resolving the first waiter, want 1 pending waiter, got %d", len(pending))
	}
	if pending[0].ID == "" {
		t.Fatal("second waiter is still registered under the colliding empty ID")
	}
	if pending[0].ToolName != "toolB" {
		t.Fatalf("surviving waiter is %q, want toolB", pending[0].ToolName)
	}
	if _, ok := b.ResolveApproval(pending[0].ID, permission.Deny); !ok {
		t.Fatalf("re-keyed waiter %q is not resolvable", pending[0].ID)
	}

	if d := <-gotA; d != permission.Allow {
		t.Fatalf("waiter A received %v, want Allow (decision misrouted)", d)
	}
	if d := <-gotB; d != permission.Deny {
		t.Fatalf("waiter B received %v, want Deny (decision misrouted)", d)
	}
}

// #4151: the ask_user path shares the same overwrite hazard.
func TestAwaitAskUserSameIDDoesNotOverwriteWaiter(t *testing.T) {
	b := NewInteractionBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gotA := make(chan toolpkg.AskUserResponse, 1)
	gotB := make(chan toolpkg.AskUserResponse, 1)
	go func() {
		r, _ := b.AwaitAskUser(ctx, AskUserRequest{ID: ""})
		gotA <- r
	}()
	waitForAskMapLen(t, b, 1)
	go func() {
		r, _ := b.AwaitAskUser(ctx, AskUserRequest{ID: ""})
		gotB <- r
	}()
	waitForAskMapLen(t, b, 2)

	if _, ok := b.ResolveAskUser("", toolpkg.AskUserResponse{Status: toolpkg.AskUserStatusSubmitted}); !ok {
		t.Fatal("first ask waiter (original empty ID) is not resolvable")
	}
	pending := b.PendingAskUsers()
	if len(pending) != 1 {
		t.Fatalf("after resolving the first waiter, want 1 pending ask waiter, got %d", len(pending))
	}
	if pending[0].ID == "" {
		t.Fatal("second ask waiter is still registered under the colliding empty ID")
	}
	if _, ok := b.ResolveAskUser(pending[0].ID, toolpkg.AskUserResponse{Status: toolpkg.AskUserStatusCancelled}); !ok {
		t.Fatalf("re-keyed ask waiter %q is not resolvable", pending[0].ID)
	}

	if r := <-gotA; r.Status != toolpkg.AskUserStatusSubmitted {
		t.Fatalf("ask waiter A received %+v, want Submitted (response misrouted)", r)
	}
	if r := <-gotB; r.Status != toolpkg.AskUserStatusCancelled {
		t.Fatalf("ask waiter B received %+v, want Cancelled (response misrouted)", r)
	}
}
