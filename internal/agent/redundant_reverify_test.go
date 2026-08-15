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

	// First test run passes
	hint1 := s.recordToolCall("run_command", "go test ./...", 1, false)
	if hint1 != "" {
		t.Fatalf("first run should not trigger: %s", hint1)
	}

	// Second test run without edits -> should trigger
	hint2 := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint2 == "" {
		t.Fatal("second verification without edits should trigger hint")
	}
}

func TestRedundantReverifyNoTriggerAfterEdit(t *testing.T) {
	s := newRedundantReverifyState()

	// First test run
	s.recordToolCall("run_command", "go test ./...", 1, false)

	// Make an edit
	s.recordEdit("edit_file")

	// Second test run should NOT trigger (edits happened)
	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint != "" {
		t.Errorf("verification after edits should not trigger hint: %s", hint)
	}
}

func TestRedundantReverifyNoTriggerAfterFailedRun(t *testing.T) {
	s := newRedundantReverifyState()

	// First test run FAILS
	s.recordToolCall("run_command", "go test ./...", 1, true)

	// Second test run without edits -> should NOT trigger because prev was error
	// (agent may re-run to reproduce or check after diagnostic changes)
	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint != "" {
		t.Errorf("re-run after failed verification should not trigger hint: %s", hint)
	}
}

func TestRedundantReverifyMaxWarnings(t *testing.T) {
	s := newRedundantReverifyState()

	// Run test repeatedly without edits: iter 1 is first, iter 2+ are redundant.
	// With max=2, hints should fire on iterations 2 and 3, then be capped.
	s.recordToolCall("run_command", "go test ./...", 1, false)

	hint2 := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint2 == "" {
		t.Fatal("expected hint on 2nd redundant run")
	}

	hint3 := s.recordToolCall("run_command", "go test ./...", 3, false)
	if hint3 == "" {
		t.Fatal("expected hint on 3rd redundant run")
	}

	// 4th redundant run should be capped
	hint4 := s.recordToolCall("run_command", "go test ./...", 4, false)
	if hint4 != "" {
		t.Errorf("hint should be capped at %d warnings, got: %s", redundantReverifyMaxWarnings, hint4)
	}
}

func TestRedundantReverifyReset(t *testing.T) {
	s := newRedundantReverifyState()

	// Record a run and trigger a warning
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

	// Build test
	s.recordToolCall("run_command", "go build ./...", 1, false)

	// Test run (different category) should not trigger
	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint != "" {
		t.Errorf("different verification category should not trigger: %s", hint)
	}
}

func TestRedundantReverifyRecordEditIgnoresNonEditTools(t *testing.T) {
	s := newRedundantReverifyState()

	// First test run
	s.recordToolCall("run_command", "go test ./...", 1, false)

	// Non-edit tool should not count as edit
	s.recordEdit("read_file")
	s.recordEdit("grep")
	s.recordEdit("run_command")

	// Should still trigger since no real edits happened
	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if hint == "" {
		t.Fatal("non-edit tools should not reset editsSince counter")
	}
}

func TestRedundantReverifyCrossLanguageBuild(t *testing.T) {
	s := newRedundantReverifyState()

	// cargo build
	s.recordToolCall("run_command", "cargo build", 1, false)
	// cargo build again without edits
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

// #343: text operations whose arguments mention verification commands are
// data, not execution — and must not poison the first real verification run.
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
	// First real go test after the grep above must NOT be flagged redundant.
	if hint := s.recordToolCall("run_command", "go test ./...", 1, false); hint != "" {
		t.Fatalf("first real verification falsely flagged: %s", hint)
	}
	// Actual redundant re-run still detected.
	if hint := s.recordToolCall("run_command", "go test ./...", 2, false); hint == "" {
		t.Fatal("genuine redundant re-run not flagged")
	}
}
