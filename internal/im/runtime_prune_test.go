package im

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestPruneApprovalsSendsDeny(t *testing.T) {
	mgr := NewManager()

	stale := time.Now().Add(-2 * time.Hour)
	const staleCount = 40
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
