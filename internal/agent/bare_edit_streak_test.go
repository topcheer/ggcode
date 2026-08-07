package agent

import "testing"

func TestBareEditStreak_NoWarningBelowThreshold(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold-1; i++ {
		s.recordToolCall("edit_file")
	}
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning below threshold, got: %s", msg)
	}
}

func TestBareEditStreak_WarnsAtThreshold(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold; i++ {
		s.recordToolCall("edit_file")
	}
	msg := s.maybeWarn(1)
	if msg == "" {
		t.Fatal("expected warning at threshold, got none")
	}
}

func TestBareEditStreak_VerificationResetsStreak(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold; i++ {
		s.recordToolCall("edit_file")
	}
	s.recordToolCall("run_command") // verification resets
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning after verification, got: %s", msg)
	}
	if s.streak != 0 {
		t.Fatalf("expected streak=0 after verification, got %d", s.streak)
	}
}

func TestBareEditStreak_NeutralToolsDontCount(t *testing.T) {
	s := newBareEditState()
	s.recordToolCall("read_file")
	s.recordToolCall("grep")
	if s.streak != 0 {
		t.Fatalf("neutral tools should not affect streak, got %d", s.streak)
	}
}

func TestBareEditStreak_MutationToolsIncrement(t *testing.T) {
	tools := []string{"edit_file", "multi_edit_file", "write_file",
		"multi_file_write", "multi_file_edit", "file_ops", "notebook_edit"}
	for _, tool := range tools {
		if !bareStreakIsMutation(tool) {
			t.Errorf("expected %s to be mutation", tool)
		}
	}
}

func TestBareEditStreak_VerificationToolsReset(t *testing.T) {
	tools := []string{"run_command", "start_command", "lsp_diagnostics",
		"lsp_references", "lsp_definition", "code_health", "review_changes",
		"verify", "git_diff", "git_status"}
	for _, tool := range tools {
		if !bareStreakIsVerification(tool) {
			t.Errorf("expected %s to be verification", tool)
		}
	}
}

func TestBareEditStreak_MaxWarns(t *testing.T) {
	s := newBareEditState()
	// Fire warnings until capped
	for i := 0; i < bareEditStreakMaxWarns+2; i++ {
		// grow streak by large amount to trigger re-warn gap
		for j := 0; j < bareEditStreakRewarnGap+1; j++ {
			s.recordToolCall("edit_file")
		}
		s.maybeWarn(1)
	}
	if s.warnCount != bareEditStreakMaxWarns {
		t.Fatalf("expected warnCount=%d, got %d", bareEditStreakMaxWarns, s.warnCount)
	}
}

func TestBareEditStreak_Reset(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold; i++ {
		s.recordToolCall("edit_file")
	}
	s.maybeWarn(1)
	s.reset()
	if s.streak != 0 || s.warnCount != 0 || s.lastWarnedAt != 0 {
		t.Fatal("reset should zero all fields")
	}
}
