package im

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

// TestPruneApprovalsSendsDeny verifies that when pruneApprovalsLocked evicts
// an abandoned (unresolved, >1h old) approval, any consumer blocked on the
// response channel reads an explicit Deny instead of the channel's zero
// value (permission.Allow), which would fail open (#259).
func TestPruneApprovalsSendsDeny(t *testing.T) {
	mgr := NewManager()

	stale := time.Now().Add(-2 * time.Hour)
	const staleCount = 40 // exceed the 32-entry prune threshold
	var firstCh <-chan permission.Decision
	for i := 0; i < staleCount; i++ {
		_, ch := mgr.RegisterApproval(ApprovalRequest{
			ToolName:    "run_command",
			Input:       `{"command":"ls"}`,
			RequestedAt: stale,
		})
		if i == 0 {
			firstCh = ch
		}
	}

	// This registration triggers pruneApprovalsLocked, which evicts the stale
	// entries above. Before the fix, the response channel was closed without a
	// decision, so a consumer received permission.Allow (zero value).
	mgr.RegisterApproval(ApprovalRequest{
		ToolName:    "run_command",
		Input:       `{"command":"pwd"}`,
		RequestedAt: time.Now(),
	})

	select {
	case d, ok := <-firstCh:
		if !ok {
			t.Fatal("pruned approval channel closed without a decision")
		}
		if d != permission.Deny {
			t.Fatalf("expected Deny on pruned approval channel, got %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for decision on pruned approval channel")
	}
}
