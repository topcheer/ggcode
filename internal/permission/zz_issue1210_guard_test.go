package permission

import (
	"encoding/json"
	"testing"
)

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
