package agent

import (
	"testing"
)

func TestRedundantReverifyClassify(t *testing.T) {
	s := newRedundantReverifyState()

	tests := []struct {
		tool string
		args string
		want string
	}{
		{"run_command", "go build -tags goolm ./...", "build"},
		{"run_command", "go test ./internal/agent/", "test"},
		{"run_command", "golangci-lint run", "lint"},
		{"run_command", "tsc --noEmit", "typecheck"},
		{"run_command", "gofmt -l .", "fmtcheck"},
		{"run_command", "npm test", "test"},
		{"run_command", "make build", "build"},
		{"read_file", "/some/file.go", ""},
		{"edit_file", "/some/file.go", ""},
	}

	for _, tc := range tests {
		got := s.classifyVerificationCommand(tc.tool, tc.args)
		if got != tc.want {
			t.Errorf("classify(%q, %q) = %q, want %q", tc.tool, tc.args, got, tc.want)
		}
	}
}

func TestRedundantReverifyNoFalsePositiveOnFirstRun(t *testing.T) {
	s := newRedundantReverifyState()
	hint := s.recordToolCall("run_command", "go test ./...", 1, false)
	if hint != "" {
		t.Errorf("first verification should not trigger hint, got: %s", hint)
	}
}

func TestRedundantReverifyDetectsRedundantRerun(t *testing.T) {
	s := newRedundantReverifyState()

	hint1 := s.recordToolCall("run_command", "go test ./...", 1, false)
	if hint1 != "" {
		t.Fatalf("first run should not trigger: %s", hint1)
	}

	hint2 := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint2 == "" {
		t.Fatal("second verification without edits should trigger hint")
	}
}

func TestRedundantReverifyNoTriggerAfterEdit(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "go test ./...", 1, false)

	s.recordEdit("edit_file")

	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint != "" {
		t.Errorf("verification after edits should not trigger hint: %s", hint)
	}
}

func TestRedundantReverifyNoTriggerAfterFailedRun(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "go test ./...", 1, true)

	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint != "" {
		t.Errorf("re-run after failed verification should not trigger hint: %s", hint)
	}
}

func TestRedundantReverifyMaxWarnings(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "go test ./...", 1, false)

	hint2 := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint2 == "" {
		t.Fatal("expected hint on 2nd redundant run")
	}

	hint3 := s.recordToolCall("run_command", "go test ./...", 3, false)
	if hint3 == "" {
		t.Fatal("expected hint on 3rd redundant run")
	}

	hint4 := s.recordToolCall("run_command", "go test ./...", 4, false)
	if hint4 != "" {
		t.Errorf("hint should be capped at %d warnings, got: %s", redundantReverifyMaxWarnings, hint4)
	}
}

func TestRedundantReverifyReset(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "go test ./...", 1, false)
	s.recordToolCall("run_command", "go test ./...", 2, false)

	if s.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warnings)
	}

	s.reset()

	if s.warnings != 0 {
		t.Errorf("after reset, expected 0 warnings, got %d", s.warnings)
	}
	if len(s.lastRun) != 0 {
		t.Errorf("after reset, expected 0 lastRun entries, got %d", len(s.lastRun))
	}
}

func TestRedundantReverifyDifferentCategoryNoTrigger(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "go build ./...", 1, false)

	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint != "" {
		t.Errorf("different verification category should not trigger: %s", hint)
	}
}

func TestRedundantReverifyRecordEditIgnoresNonEditTools(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "go test ./...", 1, false)

	s.recordEdit("read_file")
	s.recordEdit("grep")
	s.recordEdit("run_command")

	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint == "" {
		t.Fatal("non-edit tools should not reset editsSince counter")
	}
}

func TestRedundantReverifyCrossLanguageBuild(t *testing.T) {
	s := newRedundantReverifyState()

	s.recordToolCall("run_command", "cargo build", 1, false)

	hint := s.recordToolCall("run_command", "cargo build", 2, false)
	if hint == "" {
		t.Fatal("cargo build re-run without edits should trigger")
	}
}

func TestMaybeWarnRedundantReverifyReturnsEmpty(t *testing.T) {
	a := &Agent{redundantReverify: newRedundantReverifyState()}
	if got := a.maybeWarnRedundantReverify("tests pass"); got != "" {
		t.Errorf("maybeWarnRedundantReverify should always return empty, got: %s", got)
	}
}

func TestReverifyTextOpsNotVerification(t *testing.T) {
	s := newRedundantReverifyState()
	if cat := s.classifyVerificationCommand("run_command", `grep -rn "go test" docs/`); cat != "" {
		t.Fatalf("grep mentioning go test misclassified as %q", cat)
	}
	if cat := s.classifyVerificationCommand("run_command", `sed 's/go build/go test/' f.txt`); cat != "" {
		t.Fatalf("sed misclassified as %q", cat)
	}
	if cat := s.classifyVerificationCommand("grep", `"pattern": "go test ./..."`); cat != "" {
		t.Fatalf("non-shell tool misclassified as %q", cat)
	}

	if hint := s.recordToolCall("run_command", "go test ./...", 1, false); hint != "" {
		t.Fatalf("first real verification falsely flagged: %s", hint)
	}

	if hint := s.recordToolCall("run_command", "go test ./...", 2, false); hint == "" {
		t.Fatal("genuine redundant re-run not flagged")
	}
}

func TestRecordShellSourceMutationSuppressesReverifyWarning(t *testing.T) {
	s := newRedundantReverifyState()
	args := `{"command":"go test ./..."}`
	if w := s.recordToolCall("run_command", args, 1, false); w != "" {
		t.Fatalf("first run must not warn: %s", w)
	}
	s.recordShellSourceMutation()
	if w := s.recordToolCall("run_command", args, 2, false); w != "" {
		t.Fatalf("re-run after shell source mutation must not warn (source changed): %s", w)
	}
}

func TestReverifyBumpsOnTreeMutations(t *testing.T) {
	s := newRedundantReverifyState()
	s.lastRun["test"] = &reverifyRun{toolName: "run_command", iteration: 1}
	for _, tool := range []string{"git_checkout", "git_stash", "git_reset", "git_revert", "undo_edit"} {
		s.recordEdit(tool)
	}
	if got := s.lastRun["test"].editsSince; got != 5 {
		t.Fatalf("tree-mutating tools must bump editsSince, got %d want 5 (#1679 case 2)", got)
	}

	s.recordEdit("read_file")
	if got := s.lastRun["test"].editsSince; got != 5 {
		t.Fatalf("read_file must not bump, got %d", got)
	}
}
