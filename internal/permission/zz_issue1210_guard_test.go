package permission

import (
	"encoding/json"
	"testing"
)

// TestIssue1210Guard_SwitchModeAllowedInEveryMode pins the self-rescue
// channel invariant: switch_mode must be allowed by Check() in EVERY
// permission mode, especially PlanMode. This safety property depends on
// the IsAlwaysAllowedTool check running BEFORE the mode switch in
// ConfigPolicy.Check() - reordering the function or moving the whitelist
// into the per-mode branches would silently trap an autonomous agent in
// plan mode (it would be denied everything, including the only tool that
// can change the mode). Related: #1209, #1210.
func TestIssue1210Guard_SwitchModeAllowedInEveryMode(t *testing.T) {
	for _, mode := range ValidPermissionModes {
		p := NewConfigPolicyWithMode(nil, nil, mode)
		d, err := p.Check("switch_mode", json.RawMessage(`{"mode":"auto"}`))
		if err != nil {
			t.Fatalf("%s: Check(switch_mode) error: %v", mode, err)
		}
		if d != Allow {
			t.Errorf("%s: Check(switch_mode) = %v, want Allow - self-rescue channel is blocked, autonomous agents would be trapped in this mode", mode, d)
		}
	}
}
