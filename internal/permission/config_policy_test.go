package permission

import (
	"encoding/json"
	"testing"
)

// TestCheckDenyFastPathConcurrentWithOverride pins #1776 case 1: the #1595-C
// Deny fast path read p.rules UNLOCKED - a concurrent SetOverride (TUI rule
// save) hit the same map and went fatal (concurrent map read and map write).
// The fast path now snapshots under a short RLock; hammer both sides.
func TestCheckDenyFastPathConcurrentWithOverride(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, nil, AutoMode)
	p.SetOverride("lanchat", Deny)
	input := json.RawMessage(`{}`)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			p.SetOverride("lanchat", Deny)
			p.SetOverride("other-tool", Allow)
			p.ClearOverride("other-tool")
		}
	}()
	for i := 0; i < 2000; i++ {
		if d, err := p.Check("lanchat", input); err == nil && d != Deny {
			t.Fatalf("deny rule must win on the fast path, got %v", d)
		}
	}
	<-done
}
