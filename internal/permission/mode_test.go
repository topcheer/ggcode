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
		{AutoMode, "supervised"},
		{BypassMode, "bypass"},
		{AutopilotMode, "bypass"},
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
		{"auto", SupervisedMode},
		{"bypass", BypassMode},
		{"autopilot", BypassMode},
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

	d, err := policy.Check("read_file", readInput)
	if err != nil || d != Allow {
		t.Errorf("PlanMode: read_file should be Allow, got %v err=%v", d, err)
	}

	d, err = policy.Check("write_file", writeInput)
	if err != nil || d != Deny {
		t.Errorf("PlanMode: write_file should be Deny, got %v err=%v", d, err)
	}

	d, err = policy.Check("run_command", cmdInput)
	if err != nil || d != Ask {
		t.Errorf("PlanMode: extremely dangerous run_command should be Ask, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", cmdInput)
	if err != nil || d != Ask {
		t.Errorf("PlanMode: extremely dangerous start_command should be Ask, got %v err=%v", d, err)
	}

	d, err = policy.Check("write_command_input", json.RawMessage(`{"job_id":"cmd-1","input":"rm -rf /"}`))
	if err != nil || d != Allow {
		t.Errorf("PlanMode: write_command_input should be Allow (#197 stdin data), got %v err=%v", d, err)
	}

	d, err = policy.Check("lanchat", json.RawMessage(`{"action":"list"}`))
	if err != nil || d != Allow {
		t.Errorf("PlanMode: lanchat should be Allow, got %v err=%v", d, err)
	}
}

func TestLanchatAlwaysAllowed(t *testing.T) {

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

	imInput := json.RawMessage(`{"action":"status"}`)
	for _, mode := range ValidPermissionModes {
		policy := NewConfigPolicyWithMode(nil, []string{"."}, mode)
		d, err := policy.Check("im", imInput)
		if err != nil || d != Allow {
			t.Errorf("%s: im status should be Allow, got %v err=%v", mode.String(), d, err)
		}
	}

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
