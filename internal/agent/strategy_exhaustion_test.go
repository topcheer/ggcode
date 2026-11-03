package agent

import (
	"strings"
	"testing"
)

func TestStrategyExhaustion_NoTriggerOnSuccess(t *testing.T) {
	s := newStrategyExhaustionState()
	for i := 0; i < 10; i++ {
		msg := s.recordToolCall("read_file", false, "", i)
		if msg != "" {
			t.Fatalf("expected no warning on successful calls, got: %s", msg)
		}
	}
}

func TestStrategyExhaustion_SameStrategyRepeatedNoTrigger(t *testing.T) {
	s := newStrategyExhaustionState()
	errContent := "undefined: foo"

	for i := 0; i < 8; i++ {
		s.recordToolCall("edit_file", false, "", i*3)
		s.recordToolCall("run_command", false, "", i*3+1)
		msg := s.recordToolCall("run_command", true, errContent, i*3+2)

		if msg != "" {
			t.Fatalf("should not trigger for repeated same strategy at iteration %d, got: %s", i, msg)
		}
	}
}

func TestStrategyExhaustion_TriggersOnDiverseStrategies(t *testing.T) {
	s := newStrategyExhaustionState()
	errContent := "build error: undefined: foo"

	s.recordToolCall("edit_file", false, "", 1)
	s.recordToolCall("run_command", false, "", 2)
	s.recordToolCall("run_command", true, errContent, 3)

	s.recordToolCall("grep", false, "", 4)
	s.recordToolCall("edit_file", false, "", 5)
	s.recordToolCall("run_command", false, "", 6)
	s.recordToolCall("run_command", true, errContent, 7)

	s.recordToolCall("lsp_definition", false, "", 8)
	s.recordToolCall("multi_edit_file", false, "", 9)
	s.recordToolCall("run_command", false, "", 10)
	s.recordToolCall("run_command", true, errContent, 11)

	s.recordToolCall("read_file", false, "", 12)
	s.recordToolCall("write_file", false, "", 13)
	s.recordToolCall("run_command", false, "", 14)
	s.recordToolCall("run_command", true, errContent, 15)

	s.recordToolCall("glob", false, "", 16)
	s.recordToolCall("file_ops", false, "", 17)
	s.recordToolCall("run_command", false, "", 18)
	msg := s.recordToolCall("run_command", true, errContent, 19)

	if msg == "" {
		t.Fatal("expected strategy exhaustion warning after 4+ distinct strategies + recurrence")
	}
	if !strings.Contains(msg, "Strategy Exhaustion") {
		t.Fatalf("warning should contain 'Strategy Exhaustion', got: %s", msg)
	}
	if !strings.Contains(msg, "distinct") {
		t.Fatalf("warning should mention distinct strategies, got: %s", msg)
	}
}

func TestStrategyExhaustion_MaxWarnings(t *testing.T) {
	s := newStrategyExhaustionState()
	errContent := "build error: undefined: foo"

	triggerExhaustion := func(iteration int) string {

		strategies := [][]string{
			{"edit_file", "run_command"},
			{"grep", "edit_file", "run_command"},
			{"lsp_definition", "multi_edit_file", "run_command"},
			{"read_file", "write_file", "run_command"},
			{"glob", "file_ops", "run_command"},
			{"code_search", "edit_file", "run_command"},
		}
		base := iteration * 10
		for _, tool := range strategies[iteration%len(strategies)] {
			s.recordToolCall(tool, false, "", base)
			base++
		}
		return s.recordToolCall("run_command", true, errContent, base)
	}

	msg1 := triggerExhaustion(0)

	_ = msg1

	warnings := 0
	for i := 0; i < 20; i++ {
		msg := triggerExhaustion(i)
		if msg != "" {
			warnings++
		}
	}
	if warnings > seMaxWarnings {
		t.Fatalf("expected at most %d warnings, got %d", seMaxWarnings, warnings)
	}
}

func TestStrategyExhaustion_ResetClearsState(t *testing.T) {
	s := newStrategyExhaustionState()
	errContent := "build error: undefined: foo"

	s.recordToolCall("run_command", true, errContent, 1)
	s.recordToolCall("edit_file", false, "", 2)
	s.recordToolCall("run_command", true, errContent, 3)

	s.reset()

	if len(s.errorClusters) != 0 {
		t.Fatalf("expected error clusters cleared after reset, got %d", len(s.errorClusters))
	}
	if len(s.toolHistory) != 0 {
		t.Fatalf("expected tool history cleared after reset, got %d", len(s.toolHistory))
	}
	if s.warningCount != 0 {
		t.Fatalf("expected warning count 0 after reset, got %d", s.warningCount)
	}
}

func TestStrategyExhaustion_DifferentErrorsNoTrigger(t *testing.T) {
	s := newStrategyExhaustionState()

	errors := []string{
		"undefined: foo",
		"undefined: bar",
		"undefined: baz",
		"type mismatch",
		"unused import",
	}
	for i, err := range errors {
		s.recordToolCall("edit_file", false, "", i*2)
		msg := s.recordToolCall("run_command", true, err, i*2+1)
		if msg != "" {
			t.Fatalf("should not trigger for different errors, got: %s", msg)
		}
	}
}

func TestStrategyExhaustion_FingerprintNormalization(t *testing.T) {

	err1 := "/path/to/file.go:42:5: undefined: foo"
	err2 := "/different/path.go:100:20: undefined: foo"

	fp1 := seFingerprintError(err1)
	fp2 := seFingerprintError(err2)

	if fp1 != fp2 {
		t.Fatalf("normalized fingerprints should match for same error type: %s vs %s", fp1, fp2)
	}
}

func TestStrategyExhaustion_FingerprintDifferentErrors(t *testing.T) {
	err1 := "undefined: foo"
	err2 := "type mismatch: int vs string"

	fp1 := seFingerprintError(err1)
	fp2 := seFingerprintError(err2)

	if fp1 == fp2 {
		t.Fatal("fingerprints should differ for different errors")
	}
}

func TestStrategyExhaustion_StrategySignature(t *testing.T) {

	sig1 := seStrategySignature([]string{"edit_file", "run_command"})
	sig2 := seStrategySignature([]string{"edit_file", "run_command"})
	if sig1 != sig2 {
		t.Fatal("identical sequences should have same signature")
	}

	sig3 := seStrategySignature([]string{"run_command", "edit_file"})
	if sig1 != sig3 {
		t.Fatal("different order of the same recovery set must have same signature")
	}

	sig4 := seStrategySignature([]string{"edit_file", "write_file"})
	if sig1 == sig4 {
		t.Fatal("different recovery tool sets should have different signature")
	}
}

func TestStrategyExhaustion_EmptyToolsNoSignature(t *testing.T) {
	s := newStrategyExhaustionState()
	errContent := "build error"

	s.recordToolCall("run_command", true, errContent, 1)

	msg := s.recordToolCall("run_command", true, errContent, 2)
	if msg != "" {
		t.Fatalf("should not trigger with empty strategy signatures, got: %s", msg)
	}

	cl := s.errorClusters[seFingerprintError(errContent)]
	if cl == nil {
		t.Fatal("expected error cluster to exist")
	}
	if len(cl.strategySignatures) != 0 {
		t.Fatalf("expected 0 strategy signatures with no intervening tools, got %d", len(cl.strategySignatures))
	}
}
