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
		{AutoMode, "auto"},
		{BypassMode, "bypass"},
		{AutopilotMode, "autopilot"},
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
		{"auto", AutoMode},
		{"bypass", BypassMode},
		{"autopilot", AutopilotMode},
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
	if SupervisedMode.Next() != PlanMode {
		t.Error("supervised.Next() should be plan")
	}
	if PlanMode.Next() != AutoMode {
		t.Error("plan.Next() should be auto")
	}
	if AutoMode.Next() != BypassMode {
		t.Error("auto.Next() should be bypass")
	}
	if BypassMode.Next() != AutopilotMode {
		t.Error("bypass.Next() should be autopilot")
	}
	if AutopilotMode.Next() != SupervisedMode {
		t.Error("autopilot.Next() should be supervised")
	}
}

func TestIsReadOnlyTool(t *testing.T) {
	readOnly := []string{"read_file", "list_directory", "search_files", "grep"}
	write := []string{"write_file", "edit_file", "run_command", "start_command", "bash"}
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

	// Run command should be denied
	d, err = policy.Check("run_command", cmdInput)
	if err != nil || d != Deny {
		t.Errorf("PlanMode: run_command should be Deny, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", cmdInput)
	if err != nil || d != Deny {
		t.Errorf("PlanMode: start_command should be Deny, got %v err=%v", d, err)
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

	d, err = policy.Check("run_command", dangerousInput)
	if err != nil || d != Deny {
		t.Errorf("AutoMode: dangerous command should be Deny, got %v err=%v", d, err)
	}
	d, err = policy.Check("start_command", dangerousInput)
	if err != nil || d != Deny {
		t.Errorf("AutoMode: dangerous start_command should be Deny, got %v err=%v", d, err)
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
