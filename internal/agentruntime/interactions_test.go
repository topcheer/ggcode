package agentruntime

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestAwaitApprovalHonorsBufferedDecisionOnCtxDone(t *testing.T) {

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
		cancel()
		if d := <-got; d != permission.Allow {
			t.Fatalf("iter %d: buffered Allow discarded on ctx.Done (got %v)", i, d)
		}
	}
}

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

func TestConcurrentDistinctIDsResolveIndependently(t *testing.T) {
	b := NewInteractionBroker()

	const waiters = 8
	perWaiter := make([]chan permission.Decision, waiters)
	for i := range perWaiter {
		perWaiter[i] = make(chan permission.Decision, 1)
	}
	for i := 0; i < waiters; i++ {
		id := fmt.Sprintf("req-%d", i)
		go func() {
			perWaiter[i] <- b.AwaitApproval(context.Background(), ApprovalRequest{ID: id, ToolName: "run_command", Input: "echo"})
		}()
	}

	deadline := time.After(5 * time.Second)
	for {
		if b.ApprovalCount() == waiters {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d registrations, got %d", waiters, b.ApprovalCount())
		case <-time.After(10 * time.Millisecond):
		}
	}

	for i := 0; i < waiters; i += 2 {
		if _, ok := b.ResolveApproval(fmt.Sprintf("req-%d", i), permission.Allow); !ok {
			t.Fatalf("resolve req-%d should hit a registered waiter", i)
		}
	}
	for i := 1; i < waiters; i += 2 {
		if _, ok := b.ResolveApproval(fmt.Sprintf("req-%d", i), permission.Deny); !ok {
			t.Fatalf("resolve req-%d should hit a registered waiter", i)
		}
	}

	for i := 0; i < waiters; i++ {
		select {
		case got := <-perWaiter[i]:
			want := permission.Deny
			if i%2 == 0 {
				want = permission.Allow
			}
			if got != want {
				t.Fatalf("waiter %d: cross-wired decision, want %v got %v", i, want, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("waiter %d never received its decision (dead waiter)", i)
		}
	}
}
