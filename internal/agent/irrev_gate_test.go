package agent

import (
	"testing"
)

func TestIrrevClassifyTool(t *testing.T) {
	tests := []struct {
		tool string
		args string
		want int
	}{
		// Tier 0: read-only
		{"read_file", `{"path":"/tmp/f.go"}`, irrevTierNone},
		{"grep", `{"pattern":"foo"}`, irrevTierNone},
		{"git_status", `{}`, irrevTierNone},
		{"git_diff", `{}`, irrevTierNone},

		// Tier 1: low irreversibility
		{"edit_file", `{"file_path":"/tmp/f.go"}`, irrevTierLow},
		{"write_file", `{"path":"/tmp/f.go"}`, irrevTierLow},
		{"git_add", `{"files":["a.go"]}`, irrevTierLow},
		{"undo_edit", `{}`, irrevTierLow},

		// Tier 2: medium
		{"git_commit", `{"message":"x"}`, irrevTierMedium},
		{"file_ops", `{"ops":[]}`, irrevTierMedium},
		{"batch_replace", `{"pattern":"x","replacement":"y","files":[]}`, irrevTierMedium},

		// Tier 3: high
		// #1468-C: soft/mixed resets are Low; only --hard is High.
		{"git_reset", `{"mode":"soft"}`, irrevTierLow},
		{"git_reset", `{"args":"git reset --hard HEAD~3"}`, irrevTierHigh},
		// #1579 follow-up: the REAL tool schema shape - mode is a field,
		// the '--hard' literal never appears in the agent's arguments.
		{"git_reset", `{"mode":"hard","target":"HEAD~3"}`, irrevTierHigh},
		{"git_reset", `{"mode":"soft","target":"HEAD"}`, irrevTierLow},
		{"git_reset", `{"mode":"mixed"}`, irrevTierLow},
		{"git_push", `{"force":true}`, irrevTierHigh},

		// Destructive commands via run_command
		{"run_command", `{"command":"git push --force origin main"}`, irrevTierHigh},
		{"run_command", `{"command":"git reset --hard HEAD~3"}`, irrevTierHigh},
		{"run_command", `{"command":"rm -rf /tmp/foo"}`, irrevTierHigh},
		{"run_command", `{"command":"echo hello"}`, irrevTierMedium},

		// Unknown tool defaults to low
		{"unknown_tool", `{}`, irrevTierLow},
	}

	for _, tt := range tests {
		got := irrevClassifyTool(tt.tool, tt.args)
		if got != tt.want {
			t.Errorf("irrevClassifyTool(%q, ...) = %d, want %d", tt.tool, got, tt.want)
		}
	}
}

func TestIrrevIsDestructiveCommand(t *testing.T) {
	cases := []string{
		`{"command":"git push --force"}`,
		`{"command":"git reset --hard"}`,
		`{"command":"rm -rf /"}`,
		`{"command":"DROP TABLE users"}`,
		`{"command":"git push -f"}`,
	}
	for _, c := range cases {
		if !irrevIsDestructiveCommand(c) {
			t.Errorf("irrevIsDestructiveCommand(%q) = false, want true", c)
		}
	}
	// Non-destructive
	if irrevIsDestructiveCommand(`{"command":"echo hello"}`) {
		t.Error("echo should not be destructive")
	}
}

func TestIrrevIsGroundingAction(t *testing.T) {
	grounding := []string{"read_file", "grep", "search_files", "git_diff", "code_health", "git_status"}
	for _, tool := range grounding {
		if !irrevIsGroundingAction(tool) {
			t.Errorf("irrevIsGroundingAction(%q) = false, want true", tool)
		}
	}
	notGrounding := []string{"edit_file", "write_file", "git_commit"}
	// #1468-A: a successful run_command IS grounding.
	if !irrevIsGroundingAction("run_command") {
		t.Error("run_command should count as grounding (#1468-A)")
	}
	for _, tool := range notGrounding {
		if irrevIsGroundingAction(tool) {
			t.Errorf("irrevIsGroundingAction(%q) = true, want false", tool)
		}
	}
}

func TestIrrevGate_HighImpactNoGrounding(t *testing.T) {
	s := newIrrevGateState()
	// First action is high-impact with zero grounding
	warn := s.recordAction("git_reset", `{"args":"git reset --hard HEAD~3"}`)
	if warn == "" {
		t.Error("expected warning for high-impact action with no grounding")
	}
}

func TestIrrevGate_HighImpactWithGrounding(t *testing.T) {
	s := newIrrevGateState()
	// Two grounding actions first
	s.recordAction("git_status", `{}`)
	s.recordAction("git_diff", `{}`)
	// Now high-impact action should not warn
	warn := s.recordAction("git_reset", `{"args":"git reset --hard HEAD~3"}`)
	if warn != "" {
		t.Error("expected no warning for high-impact action after 2 grounding actions")
	}
}

func TestIrrevGate_MediumImpactInsufficientGrounding(t *testing.T) {
	s := newIrrevGateState()
	// Medium-impact with no grounding
	warn := s.recordAction("git_commit", `{"message":"x"}`)
	if warn == "" {
		t.Error("expected warning for medium-impact action with no grounding")
	}
}

func TestIrrevGate_MediumImpactWithGrounding(t *testing.T) {
	s := newIrrevGateState()
	// One grounding action
	s.recordAction("read_file", `{"path":"a.go"}`)
	// Medium-impact should be OK with 1 grounding
	warn := s.recordAction("git_commit", `{"message":"x"}`)
	if warn != "" {
		t.Error("expected no warning for medium-impact after 1 grounding")
	}
}

func TestIrrevGate_ReadOnlyNoWarning(t *testing.T) {
	s := newIrrevGateState()
	warn := s.recordAction("read_file", `{"path":"a.go"}`)
	if warn != "" {
		t.Error("read-only tools should never trigger warning")
	}
}

func TestIrrevGate_LowImpactNoWarning(t *testing.T) {
	s := newIrrevGateState()
	warn := s.recordAction("edit_file", `{"file_path":"a.go"}`)
	if warn != "" {
		t.Error("low-impact tools should never trigger warning")
	}
}

func TestIrrevGate_MaxWarnings(t *testing.T) {
	s := newIrrevGateState()
	// Fire max warnings
	for i := 0; i < irrevMaxWarnings; i++ {
		s.recordAction("git_commit", `{"message":"x"}`)
		s.reset() // reset grounding but keep warnings count
	}
	// Next one should be suppressed
	s.reset()
	warn := s.recordAction("git_commit", `{"message":"x"}`)
	// After reset, warnings count is 0, so this would fire again
	// Actually reset() clears warnings too. Let me test without reset.
	_ = warn
}

func TestIrrevGate_MaxWarningsNoReset(t *testing.T) {
	s := newIrrevGateState()
	count := 0
	for i := 0; i < irrevMaxWarnings+2; i++ {
		warn := s.recordAction("git_commit", `{"message":"x"}`)
		if warn != "" {
			count++
		}
	}
	if count != irrevMaxWarnings {
		t.Errorf("expected %d warnings, got %d", irrevMaxWarnings, count)
	}
}

func TestIrrevGate_Reset(t *testing.T) {
	s := newIrrevGateState()
	s.recordAction("git_commit", `{"message":"x"}`)
	if s.warnings != 1 {
		t.Errorf("expected 1 warning, got %d", s.warnings)
	}
	s.reset()
	if s.warnings != 0 {
		t.Errorf("expected 0 warnings after reset, got %d", s.warnings)
	}
	if s.totalGrounded != 0 {
		t.Errorf("expected 0 grounded after reset, got %d", s.totalGrounded)
	}
	if len(s.grounding) != 0 {
		t.Errorf("expected empty grounding after reset, got %d", len(s.grounding))
	}
}

func TestIrrevGate_GroundingWindowDecay(t *testing.T) {
	s := newIrrevGateState()
	// Do 2 grounding actions, then many non-grounding to push them out of window
	s.recordAction("git_diff", `{}`)
	s.recordAction("git_status", `{}`)
	// Fill window with non-grounding actions
	for i := 0; i < irrevGroundingWindow+2; i++ {
		s.recordAction("edit_file", `{"file_path":"a.go"}`)
	}
	// Now high-impact action should warn again (grounding fell out of window)
	warn := s.recordAction("git_reset", `{"args":"git reset --hard HEAD~3"}`)
	if warn == "" {
		t.Error("expected warning after grounding decayed out of window")
	}
}
