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

// TestMemoryToolAlwaysAllowed pins the fast-path approval of the Anthropic
// Memory Tool: it writes only to its confined per-project store
// (internal/agent/memory_tool.go), the memory-tool analogue of save_memory,
// so it must be allowed without prompting in every permission mode -
// including plan mode, where the model may legitimately record findings.
func TestMemoryToolAlwaysAllowed(t *testing.T) {
	modes := []PermissionMode{SupervisedMode, PlanMode, AutoMode, BypassMode, AutopilotMode}
	for _, mode := range modes {
		p := NewConfigPolicyWithMode(nil, nil, mode)
		d, err := p.Check("memory", json.RawMessage(`{"command":"view","path":"/memories/x.txt"}`))
		if err != nil || d != Allow {
			t.Fatalf("memory tool must be allowed in mode %d: got %v err=%v", mode, d, err)
		}
	}
}
