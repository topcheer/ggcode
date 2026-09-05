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
