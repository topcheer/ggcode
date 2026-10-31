package permission

import (
	"encoding/json"
	"testing"
)

func TestPermissionModeString(t *testing.T) {
	tests := []struct {
		mode PermissionMode
		want string
	}{
		{SupervisedMode, "supervised"},
		{PlanMode, "plan"},
		{AutoMode, "supervised"}, // legacy alias normalizes
		{BypassMode, "bypass"},
		{AutopilotMode, "bypass"}, // legacy alias normalizes
		{PermissionMode(99), "supervised"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("PermissionMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestParsePermissionMode(t *testing.T) {
	tests := []struct {
		input string
		want  PermissionMode
	}{
		{"plan", PlanMode},
		{"Plan", PlanMode},
		{"PLAN", PlanMode},
		{"auto", SupervisedMode}, // legacy alias → canonical
		{"bypass", BypassMode},
		{"autopilot", BypassMode}, // legacy alias → canonical
		{"supervised", SupervisedMode},
		{"unknown", SupervisedMode},
	}
	for _, tt := range tests {
		if got := ParsePermissionMode(tt.input); got != tt.want {
			t.Errorf("ParsePermissionMode(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestPermissionModeNext(t *testing.T) {
	// Two-mode cycle: supervised ↔ bypass. Legacy aliases normalize into
	// the cycle; plan is tool-internal and cycles back to supervised.
	if SupervisedMode.Next() != BypassMode {
		t.Error("supervised.Next() should be bypass")
	}
	if BypassMode.Next() != SupervisedMode {
		t.Error("bypass.Next() should be supervised")
	}
	if AutoMode.Next() != BypassMode {
		t.Error("auto(alias).Next() should be bypass")
	}
	if AutopilotMode.Next() != SupervisedMode {
		t.Error("autopilot(alias).Next() should be supervised")
	}
	if PlanMode.Next() != SupervisedMode {
		t.Error("plan.Next() should be supervised (tool-internal, not cycled)")
	}
}

func TestIsReadOnlyTool(t *testing.T) {
	readOnly := []string{"read_file", "multi_file_read", "list_directory", "search_files", "grep", "code_search"}
	write := []string{"write_file", "edit_file", "multi_edit_file", "multi_file_edit", "run_command", "start_command", "write_command_input", "bash"}
	for _, name := range readOnly {
		if !IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) should be true", name)
		}
	}
	for _, name := range write {
		if IsReadOnlyTool(name) {
			t.Errorf("IsReadOnlyTool(%q) should be false", name)
		}
	}
}

func TestPlanModeDeniesWrites(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, PlanMode)

	readInput := json.RawMessage(`{"file_path":"test.go"}`)
	writeInput := json.RawMessage(`{"file_path":"test.go","content":"hello"}`)
	cmdInput := json.RawMessage(`{"command":"rm -rf /"}`)

	// Read tools should be allowed
	d, err := policy.Check("read_file", readInput)
	if err != nil || d != Allow {
		t.Errorf("PlanMode: read_file should be Allow, got %v err=%v", d, err)
	}

	// Write tools should be denied
	d, err = policy.Check("write_file", writeInput)
	if err != nil || d != Deny {
		t.Errorf("PlanMode: write_file should be Deny, got %v err=%v", d, err)
	}

	// two-mode refactor: plan mode ALLOWS command tools (only explicit
	// file-writing tools are denied); the extremely-dangerous floor still
	// gates (cmdInput is "rm -rf /" elsewhere in this test — Ask).
	d, err = policy.Check("run_command", cmdInput)
	if err != nil || d != Ask {
		t.Errorf("PlanMode: extremely dangerous run_command should be Ask, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", cmdInput)
	if err != nil || d != Ask {
		t.Errorf("PlanMode: extremely dangerous start_command should be Ask, got %v err=%v", d, err)
	}
	// write_command_input is not an explicit file-writing tool and its
	// payload is stdin data, not a command (#197) — Allow in plan mode.
	d, err = policy.Check("write_command_input", json.RawMessage(`{"job_id":"cmd-1","input":"rm -rf /"}`))
	if err != nil || d != Allow {
		t.Errorf("PlanMode: write_command_input should be Allow (#197 stdin data), got %v err=%v", d, err)
	}

	// lanchat should be allowed in plan mode (always-allowed tool)
	d, err = policy.Check("lanchat", json.RawMessage(`{"action":"list"}`))
	if err != nil || d != Allow {
		t.Errorf("PlanMode: lanchat should be Allow, got %v err=%v", d, err)
	}
}

func TestLanchatAlwaysAllowed(t *testing.T) {
	// lanchat must be allowed in every permission mode without approval
	lanchatInput := json.RawMessage(`{"action":"list"}`)
	for _, mode := range ValidPermissionModes {
		policy := NewConfigPolicyWithMode(nil, []string{"."}, mode)
		d, err := policy.Check("lanchat", lanchatInput)
		if err != nil || d != Allow {
			t.Errorf("%s: lanchat should be Allow, got %v err=%v", mode.String(), d, err)
		}
	}
}

func TestIMToolAlwaysAllowed(t *testing.T) {
	// im BENIGN ACTIONS must be allowed in every permission mode without
	// approval (#1283: the blanket always-allow was removed; only local
	// adapter-management actions stay on the fast path).
	imInput := json.RawMessage(`{"action":"status"}`)
	for _, mode := range ValidPermissionModes {
		policy := NewConfigPolicyWithMode(nil, []string{"."}, mode)
		d, err := policy.Check("im", imInput)
		if err != nil || d != Allow {
			t.Errorf("%s: im status should be Allow, got %v err=%v", mode.String(), d, err)
		}
	}
	// two-mode refactor: plan mode no longer blanket-denies non-write
	// tools, so im send_file comes back Allow there. The exfiltration
	// floor lives in the command-tool gate; im send_file of a local temp
	// file inside plan is permitted by design now.
	plan := NewConfigPolicyWithMode(nil, []string{"."}, PlanMode)
	sendFile := json.RawMessage(`{"action":"send_file","path":"/tmp/secret.png"}`)
	if d, _ := plan.Check("im", sendFile); d != Allow {
		t.Errorf("PlanMode: im send_file should be Allow (two-mode refactor; plan only denies file-writing tools), got %v", d)
	}
}

func TestAutoModeDeniesDangerous(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, AutoMode)

	safeInput := json.RawMessage(`{"command":"ls -la"}`)
	dangerousInput := json.RawMessage(`{"command":"rm -rf /"}`)

	d, err := policy.Check("run_command", safeInput)
	if err != nil || d != Allow {
		t.Errorf("AutoMode: safe command should be Allow, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", safeInput)
	if err != nil || d != Allow {
		t.Errorf("AutoMode: safe start_command should be Allow, got %v err=%v", d, err)
	}
	d, err = policy.Check("write_command_input", json.RawMessage(`{"job_id":"cmd-1","input":"echo ok"}`))
	if err != nil || d != Allow {
		t.Errorf("AutoMode: safe write_command_input should be Allow, got %v err=%v", d, err)
	}

	d, err = policy.Check("run_command", dangerousInput)
	if err != nil || d != Ask {
		t.Errorf("AutoMode(alias): extremely dangerous command should be Ask, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", dangerousInput)
	if err != nil || d != Ask {
		t.Errorf("AutoMode(alias): extremely dangerous start_command should be Ask, got %v err=%v", d, err)
	}
	// #197: write_command_input's input field is arbitrary stdin data, not a
	// command — no longer command-screened (the job was screened when started).
	d, err = policy.Check("write_command_input", json.RawMessage(`{"job_id":"cmd-1","input":"rm -rf /"}`))
	if err != nil || d != Allow {
		t.Errorf("AutoMode: write_command_input should not be command-screened (#197), got %v err=%v", d, err)
	}
}

func TestAutopilotModeMatchesBypassPermissions(t *testing.T) {
	policy := NewConfigPolicyWithMode(nil, []string{"."}, AutopilotMode)

	safeInput := json.RawMessage(`{"command":"ls -la"}`)
	dangerousInput := json.RawMessage(`{"command":"sudo rm -rf /"}`)

	d, err := policy.Check("run_command", safeInput)
	if err != nil || d != Allow {
		t.Errorf("AutopilotMode: safe command should be Allow, got %v err=%v", d, err)
	}

	d, err = policy.Check("run_command", dangerousInput)
	if err != nil || d != Ask {
		t.Errorf("AutopilotMode: extremely dangerous command should be Ask, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", dangerousInput)
	if err != nil || d != Ask {
		t.Errorf("AutopilotMode: extremely dangerous start_command should be Ask, got %v err=%v", d, err)
	}
	// #197: write_command_input is not command-screened anymore (stdin data).
	d, err = policy.Check("write_command_input", json.RawMessage(`{"job_id":"cmd-1","input":"sudo rm -rf /"}`))
	if err != nil || d != Allow {
		t.Errorf("AutopilotMode: write_command_input should not be command-screened (#197), got %v err=%v", d, err)
	}
}

func TestConfigPolicyModeGetSet(t *testing.T) {
	policy := NewConfigPolicy(nil, []string{"."})
	if policy.Mode() != SupervisedMode {
		t.Errorf("default mode should be SupervisedMode, got %v", policy.Mode())
	}

	policy.SetMode(PlanMode)
	if policy.Mode() != PlanMode {
		t.Errorf("after SetMode(Plan), should be PlanMode, got %v", policy.Mode())
	}
}
