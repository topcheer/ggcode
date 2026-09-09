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

// TestIssue1705_EscalationAsks pins #1705 case 1: switching INTO
// bypass/autopilot asks for consent in every mode - the zero-confirmation
// self-escalation is closed - while the #1210 self-rescue invariant (every
// other target Allow, incl. plan escapes) still holds.
func TestIssue1705_EscalationAsks(t *testing.T) {
	for _, mode := range ValidPermissionModes {
		p := NewConfigPolicyWithMode(nil, nil, mode)
		for _, target := range []string{"bypass", "autopilot"} {
			d, err := p.Check("switch_mode", json.RawMessage(`{"mode":"`+target+`"}`))
			if err != nil {
				t.Fatalf("%s->%s: %v", mode, target, err)
			}
			if d != Ask {
				t.Fatalf("%s->%s must Ask (escalation consent), got %v", mode, target, d)
			}
		}
		// Downgrade/escape targets keep the self-rescue Allow.
		for _, target := range []string{"plan", "supervised", "auto"} {
			d, err := p.Check("switch_mode", json.RawMessage(`{"mode":"`+target+`"}`))
			if err != nil {
				t.Fatalf("%s->%s: %v", mode, target, err)
			}
			if d != Allow {
				t.Fatalf("%s->%s must stay Allow (self-rescue), got %v", mode, target, d)
			}
		}
	}
}
