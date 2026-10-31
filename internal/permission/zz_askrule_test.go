package permission

import (
	"encoding/json"
	"testing"
)

// Review round 1 (techwriter222, medium): explicit per-tool ask rules must
// stay honored after the two-mode refactor made the default Allow.
func TestExplicitAskRuleStillAsksInEveryMode(t *testing.T) {
	for _, mode := range []PermissionMode{SupervisedMode, BypassMode, PlanMode} {
		rules := map[string]Decision{"run_command": Ask}
		p := NewConfigPolicyWithMode(rules, []string{"/workspace"}, mode)
		d, err := p.Check("run_command", json.RawMessage(`{"command":"ls"}`))
		if err != nil {
			t.Fatal(err)
		}
		if d != Ask {
			t.Fatalf("mode %v: explicit ask rule must ask, got %v", mode, d)
		}
	}
}
