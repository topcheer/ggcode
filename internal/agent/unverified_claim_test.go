package agent

import (
	"testing"
)

func TestUnverifiedClaim_NoClaims(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkUnverifiedClaim("I've updated the file as requested.", stats)
	if msg != "" {
		t.Errorf("expected empty message when no success claims, got: %s", msg)
	}
}

func TestUnverifiedClaim_ClaimsWithVerification(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1, "run_command": 1},
		FilesEdited: []string{"main.go"},
		CommandsRun: []string{"go test -tags goolm ./..."},
	}
	msg := a.checkUnverifiedClaim("All tests pass and the build is successful.", stats)
	if msg != "" {
		t.Errorf("expected empty message when verification was run, got: %s", msg)
	}
}

func TestUnverifiedClaim_ClaimsWithoutVerification(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkUnverifiedClaim("All tests pass and the build succeeds.", stats)
	if msg == "" {
		t.Fatal("expected non-empty message when claims made without verification")
	}
	if a.unverifiedClaim.fired != true {
		t.Error("expected fired flag to be set")
	}
}

func TestUnverifiedClaim_ClaimsWithDiagnosticsTool(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1, "lsp_diagnostics": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkUnverifiedClaim("Verified, no errors found.", stats)
	if msg != "" {
		t.Errorf("expected empty message when diagnostics tool used, got: %s", msg)
	}
}

func TestUnverifiedClaim_AlreadyFired(t *testing.T) {
	a := &Agent{unverifiedClaim: &unverifiedClaimState{fired: true}}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkUnverifiedClaim("All tests pass.", stats)
	if msg != "" {
		t.Errorf("expected empty when already fired, got: %s", msg)
	}
}

func TestUnverifiedClaim_Reset(t *testing.T) {
	state := &unverifiedClaimState{fired: true}
	state.reset()
	if state.fired {
		t.Error("expected fired=false after reset")
	}
}

func TestUnverifiedClaim_ShortText(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{ToolCalls: map[string]int{}}
	msg := a.checkUnverifiedClaim("ok", stats)
	if msg != "" {
		t.Errorf("expected empty for short text, got: %s", msg)
	}
}

func TestUnverifiedClaim_MultipleClaims(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls: map[string]int{"read_file": 1},
	}
	msg := a.checkUnverifiedClaim("Tests passing, build succeeds, lint clean, all green.", stats)
	if msg == "" {
		t.Fatal("expected non-empty for multiple unverified claims")
	}
}

func TestDetectSuccessClaims(t *testing.T) {
	claims := detectSuccessClaims("the tests pass and build is successful, all green")
	if len(claims) < 2 {
		t.Errorf("expected at least 2 claims, got %d: %v", len(claims), claims)
	}
}

func TestHasVerificationCommands(t *testing.T) {
	tests := []struct {
		cmds []string
		want bool
	}{
		{[]string{"go build ./..."}, true},
		{[]string{"go test ./..."}, true},
		{[]string{"npm run test"}, true},
		{[]string{"pytest"}, true},
		{[]string{"echo hello"}, false},
		{[]string{"ls -la"}, false},
		{[]string{}, false},
	}
	for _, tt := range tests {
		stats := &RunStats{CommandsRun: tt.cmds}
		if got := hasVerificationCommands(stats); got != tt.want {
			t.Errorf("hasVerificationCommands(%v) = %v, want %v", tt.cmds, got, tt.want)
		}
	}
}

func TestHasVerificationTools(t *testing.T) {
	stats := &RunStats{
		ToolCalls: map[string]int{
			"edit_file":       1,
			"lsp_diagnostics": 1,
		},
	}
	if !hasVerificationTools(stats) {
		t.Error("expected true for lsp_diagnostics")
	}

	stats2 := &RunStats{
		ToolCalls: map[string]int{"edit_file": 1},
	}
	if hasVerificationTools(stats2) {
		t.Error("expected false when no verification tools")
	}
}

func TestUnverifiedClaim_ClaimWithLintCommand(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1, "run_command": 1},
		FilesEdited: []string{"main.go"},
		CommandsRun: []string{"make lint"},
	}
	msg := a.checkUnverifiedClaim("Lint passes, no errors.", stats)
	if msg != "" {
		t.Errorf("expected empty when lint command run, got: %s", msg)
	}
}

func TestUnverifiedClaim_CodeHealthTool(t *testing.T) {
	a := &Agent{unverifiedClaim: newUnverifiedClaimState()}
	stats := &RunStats{
		ToolCalls: map[string]int{"edit_file": 1, "code_health": 1},
	}
	msg := a.checkUnverifiedClaim("Verified and validated.", stats)
	if msg != "" {
		t.Errorf("expected empty when code_health tool used, got: %s", msg)
	}
}
