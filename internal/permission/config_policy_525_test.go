package permission

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestConfigPolicyDangerousAllowDowngradeParity(t *testing.T) {
	in := json.RawMessage(`{"command":"sudo rm -rf /"}`)

	p1 := NewConfigPolicyWithMode(nil, []string{t.TempDir()}, SupervisedMode)
	rs := NewCommandRuleSet()
	rs.AddAllowPattern("sudo *")
	p1.SetCommandRuleSet(rs)
	d1, err := p1.Check("run_command", in)
	if err != nil {
		t.Fatalf("cmdRules form Check error: %v", err)
	}

	p2 := NewConfigPolicyWithMode(map[string]Decision{"run_command": Allow}, []string{t.TempDir()}, SupervisedMode)
	d2, err := p2.Check("run_command", in)
	if err != nil {
		t.Fatalf("static-rule form Check error: %v", err)
	}

	p3 := NewConfigPolicyWithMode(nil, []string{t.TempDir()}, SupervisedMode)
	d3, err := p3.Check("run_command", in)
	if err != nil {
		t.Fatalf("no-rule form Check error: %v", err)
	}

	if d1 != Ask || d2 != Ask || d3 != Ask {
		t.Errorf("dangerous-command downgrade split: cmdRules-allow=%v, static-allow=%v, no-rule=%v; all three must be Ask (allow must not be stricter than default)", d1, d2, d3)
	}

	p4 := NewConfigPolicyWithMode(map[string]Decision{"run_command": Deny}, []string{t.TempDir()}, SupervisedMode)
	d4, err := p4.Check("run_command", in)
	if err != nil {
		t.Fatalf("static-deny form Check error: %v", err)
	}
	if d4 != Deny {
		t.Errorf("explicit Deny rule + dangerous command = %v, want Deny", d4)
	}
}

func TestConfigPolicyStaticRuleSandboxDowngrade(t *testing.T) {
	sandbox := t.TempDir()
	outside := json.RawMessage(`{"file_path":"/etc/hosts","old_text":"a","new_text":"b"}`)
	insidePath := filepath.Join(sandbox, "f.txt")
	inside, err := json.Marshal(map[string]string{"file_path": insidePath, "old_text": "a", "new_text": "b"})
	if err != nil {
		t.Fatal(err)
	}
	insideRaw := json.RawMessage(inside)

	cases := []struct {
		name    string
		rule    Decision
		hasRule bool
		input   json.RawMessage
		want    Decision
	}{
		{"explicit Ask + out-of-sandbox → Ask (was hard Deny)", Ask, true, outside, Ask},
		{"explicit Allow + out-of-sandbox → Ask (not stricter than default)", Allow, true, outside, Ask},
		{"explicit Deny + out-of-sandbox → Deny (kept)", Deny, true, outside, Deny},
		{"no rule + out-of-sandbox → Ask (default control)", Allow, false, outside, Ask},
		{"explicit Allow + in-sandbox → Allow (unaffected)", Allow, true, insideRaw, Allow},
		{"explicit Ask + in-sandbox → Ask (unaffected)", Ask, true, insideRaw, Ask},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rules map[string]Decision
			if tc.hasRule {
				rules = map[string]Decision{"edit_file": tc.rule}
			}
			p := NewConfigPolicyWithMode(rules, []string{sandbox}, SupervisedMode)
			got, err := p.Check("edit_file", tc.input)
			if err != nil {
				t.Fatalf("Check error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
