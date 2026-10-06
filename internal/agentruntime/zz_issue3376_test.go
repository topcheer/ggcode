package agentruntime

// #3376 probe: CancelAll (desktop stop / run interruption) must resolve a
// pending approval with the #3372 non-decision variant - Cancelled, not
// Deny - so the audit trail, approval memory, and ask throttle never
// attribute a stopped run to the user.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestIssue3376_CancelAllResolvesApprovalAsCancelled(t *testing.T) {
	b := NewInteractionBroker()
	req := ApprovalRequest{ID: "ap-1", ToolName: "edit_file"}

	decCh := make(chan permission.Decision, 1)
	go func() {
		decCh <- b.AwaitApproval(context.Background(), req)
	}()

	// Wait until the waiter is registered, then stop the run.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		n := len(b.approvals)
		b.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	approvalReqs, _ := b.CancelAll()
	if len(approvalReqs) != 1 {
		t.Fatalf("CancelAll must report the pending approval, got %d", len(approvalReqs))
	}

	select {
	case got := <-decCh:
		if got != permission.Cancelled {
			t.Fatalf("CancelAll must resolve a pending approval as Cancelled (non-decision), got %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelAll must resolve the pending approval waiter")
	}
}
