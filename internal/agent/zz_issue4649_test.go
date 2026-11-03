package agent

import (
	"fmt"
	"testing"
)

func TestVerifyDebt_SpawnSuccessMustNotClearDebt(t *testing.T) {
	cases := []struct {
		tool string
		want bool
	}{

		{"start_command", false},

		{"run_command", true},
		{"bash", true},
		{"powershell", true},

		{"wait_command", true},
		{"read_command_output", true},

		{"read_file", false},
		{"edit_file", false},
	}
	for _, tc := range cases {
		if got := verifyDebtSpawnClearingTool(tc.tool); got != tc.want {
			t.Errorf("verifyDebtSpawnClearingTool(%q) = %v, want %v", tc.tool, got, tc.want)
		}
	}

	for _, ch := range []string{"run_command", "bash", "powershell", "wait_command", "read_command_output"} {
		if !isCommandChannelTool(ch) {
			t.Errorf("isCommandChannelTool(%q) = false, expected true (set drift)", ch)
		}
	}
}

func TestVerifyDebt_ForegroundVerifyStillClears(t *testing.T) {
	s := newVerifyDebtState()
	s.recordSourceEdit()
	s.recordSourceEdit()
	if s.editsSinceGreen != 2 {
		t.Fatalf("editsSinceGreen = %d, want 2", s.editsSinceGreen)
	}
	s.recordVerifyCommand("go test ./...", false)
	if s.editsSinceGreen != 0 {
		t.Errorf("foreground passing go test must clear editsSinceGreen, got %d", s.editsSinceGreen)
	}
}

func TestEditPropagationGateExcludesSpawn(t *testing.T) {

	if verifyDebtSpawnClearingTool("start_command") {
		t.Fatal("start_command must be excluded from the spawn-clearing gate")
	}
	if !isCommandChannelTool("run_command") {
		t.Fatal("sanity: run_command must be a command-channel tool")
	}
	if !verifyDebtSpawnClearingTool("run_command") {
		t.Error("verifyDebtSpawnClearingTool(run_command) = false, expected true (foreground channel)")
	}

	ep := newEditPropagationState()
	for i := 0; i < 3; i++ {
		ep.recordEdit("edit_file", fmt.Sprintf(`{"file_path":"/tmp/file%d.go"}`, i))
	}
	ep.mu.Lock()
	n := len(ep.distinctFiles)
	ep.mu.Unlock()
	if n != 3 {
		t.Fatalf("distinctFiles = %d, want 3 before any gate", n)
	}
	cmd := extractCommandFromArgs([]byte(`{"command":"go test ./..."}`))
	if cmd == "" || !isVerifyCommand(cmd) {
		t.Fatalf("verify-command extraction broken: cmd=%q", cmd)
	}

	if !verifyDebtSpawnClearingTool("start_command") {
		ep.mu.Lock()
		n = len(ep.distinctFiles)
		ep.mu.Unlock()
		if n != 3 {
			t.Fatal("books must survive a spawn-shaped tool result")
		}
	} else {
		t.Fatal("unreachable: predicate check above already asserted exclusion")
	}
}
