package agent

import "testing"

func TestBareEditStreak_NoWarningBelowThreshold(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold-1; i++ {
		s.recordToolCall("edit_file", "")
	}
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning below threshold, got: %s", msg)
	}
}

func TestBareEditStreak_WarnsAtThreshold(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold; i++ {
		s.recordToolCall("edit_file", "")
	}
	msg := s.maybeWarn(1)
	if msg == "" {
		t.Fatal("expected warning at threshold, got none")
	}
}

func TestBareEditStreak_VerificationResetsStreak(t *testing.T) {
	s := newBareEditState()
	for i := 0; i < bareEditStreakThreshold; i++ {
		s.recordToolCall("edit_file", "")
	}
	s.recordToolCall("run_command", `{"command":"go test"}`)
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning after verification, got: %s", msg)
	}
	if s.streak != 0 {
		t.Fatalf("expected streak=0 after verification, got %d", s.streak)
	}
}

func TestBareEditStreak_NeutralToolsDontCount(t *testing.T) {
	s := newBareEditState()
	s.recordToolCall("read_file", "")
	s.recordToolCall("grep", "")
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

	tools := []string{"lsp_diagnostics", "lsp_references", "lsp_definition",
		"code_health", "review_changes", "verify", "git_diff", "git_status"}
	for _, tool := range tools {
		if !bareStreakIsVerification(tool, "") {
			t.Errorf("expected %s to be verification", tool)
		}
	}

	verifyCmds := []string{
		`{"command":"go test ./..."}`,
		`{"command":"make verify-ci"}`,
		`{"command":"go build ./cmd/ggcode"}`,
		`{"command":"npm test"}`,
	}
	for _, input := range verifyCmds {
		if !bareStreakIsVerification("run_command", input) {
			t.Errorf("expected run_command with %s to be verification", input)
		}
	}

	nonVerifyCmds := []string{
		`{"command":"echo done"}`,
		`{"command":"ls -la"}`,
		`{"command":"pwd"}`,
		``,
	}
	for _, input := range nonVerifyCmds {
		if bareStreakIsVerification("run_command", input) {
			t.Errorf("expected run_command with %q to NOT be verification", input)
		}
	}

	gitMutations := []string{"git_add", "git_commit", "git_checkout", "git_reset", "git_revert", "git_stash"}
	for _, tool := range gitMutations {
		if bareStreakIsVerification(tool, "") {
			t.Errorf("expected %s to NOT be verification (it's a mutation)", tool)
		}
	}
}

func TestBareEditStreak_MaxWarns(t *testing.T) {
	s := newBareEditState()

	for i := 0; i < bareEditStreakMaxWarns+2; i++ {

		for j := 0; j < bareEditStreakRewarnGap+1; j++ {
			s.recordToolCall("edit_file", "")
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
		s.recordToolCall("edit_file", "")
	}
	s.maybeWarn(1)
	s.reset()
	if s.streak != 0 || s.warnCount != 0 || s.lastWarnedAt != 0 {
		t.Fatal("reset should zero all fields")
	}
}
