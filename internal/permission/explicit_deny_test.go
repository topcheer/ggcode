package permission

import "testing"

func TestExplicitDenyRuleModeIndependent(t *testing.T) {
	rules := map[string]Decision{"browser": Deny}
	for _, mode := range []PermissionMode{AutoMode, BypassMode, AutopilotMode, PlanMode} {
		p := NewConfigPolicyWithMode(rules, nil, mode)
		d, err := p.Check("browser", []byte(`{"action":"navigate","url":"file:///Users/x/.ssh/id_rsa"}`))
		if err != nil {
			t.Fatalf("mode %v: unexpected error: %v", mode, err)
		}
		if d != Deny {
			t.Errorf("mode %v: explicit deny rule for browser = %v, want Deny", mode, d)
		}
	}
}

func TestBrowserNoRuleAutoModeNotDenied(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, nil, AutoMode)
	d, err := p.Check("browser", []byte(`{"action":"navigate","url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == Deny {
		t.Errorf("browser without deny rule in AutoMode = Deny, want Allow")
	}
}
