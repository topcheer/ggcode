package agent

import (
	"strings"
	"testing"
)

// Issue #1173: two verification commands in the same category but with
// different targets must NOT be flagged as redundant re-verification.
func TestIssue1173DifferentCommandSameCategoryNotRedundant(t *testing.T) {
	s := newRedundantReverifyState()

	if hint := s.recordToolCall("run_command", "go test ./internal/a/", 1, false); hint != "" {
		t.Fatalf("first run must be silent: %s", hint)
	}
	hint := s.recordToolCall("run_command", "go test ./internal/b/", 2, false)
	if hint != "" {
		t.Fatalf("different test scope is new information, must not be flagged: %s", hint)
	}

	// Same pattern for build targets.
	s2 := newRedundantReverifyState()
	s2.recordToolCall("run_command", "go build -tags goolm ./cmd/ggcode", 1, false)
	if hint := s2.recordToolCall("run_command", "go build -tags goolm ./cmd/e2e_test", 2, false); hint != "" {
		t.Fatalf("different build target must not be flagged: %s", hint)
	}
}

// Issue #1173 companion: an identical command with no intervening edits is
// still redundant re-verification after the fix.
func TestIssue1173IdenticalCommandStillRedundant(t *testing.T) {
	s := newRedundantReverifyState()
	s.recordToolCall("run_command", "go test ./internal/a/", 1, false)
	hint := s.recordToolCall("run_command", "go test ./internal/a/", 2, false)
	if hint == "" {
		t.Fatal("identical command re-run without edits must still be flagged")
	}
}

// Issue #1173: the signature normalizes whitespace and case, so cosmetic
// differences do not defeat redundancy detection, while real differences in
// arguments do.
func TestIssue1173SignatureNormalization(t *testing.T) {
	same1 := verificationSignature("go test   ./internal/a/")
	same2 := verificationSignature("  go test ./internal/a/  ")
	same3 := verificationSignature("GO TEST ./internal/a/")
	if same1 == "" || same1 != same2 || same1 != same3 {
		t.Fatalf("cosmetic differences must normalize to same signature: %q %q %q", same1, same2, same3)
	}

	diff := verificationSignature("go test ./internal/b/")
	if diff == same1 {
		t.Fatalf("different scopes must produce different signatures: %q", same1)
	}
}

// Issue #1173: signature covers the first pipeline segment, so commands whose
// leading segment differs are never conflated even if the pipeline tail is
// identical.
func TestIssue1173PipelineFirstSegmentIsSignificant(t *testing.T) {
	s := newRedundantReverifyState()
	s.recordToolCall("run_command", "go test ./internal/a/ | tee /tmp/a.log", 1, false)
	if hint := s.recordToolCall("run_command", "go test ./internal/b/ | tee /tmp/a.log", 2, false); hint != "" {
		t.Fatalf("different leading segment must not be flagged: %s", hint)
	}
	// Re-running the SAME (previous) pipeline without edits is still detected.
	if hint := s.recordToolCall("run_command", "go test ./internal/b/ | tee /tmp/a.log", 3, false); hint == "" {
		t.Fatal("identical pipeline re-run without edits must still be flagged")
	}
}

// Issue #1173 guard: the hint text keeps mentioning the previous command so
// the message stays actionable.
func TestIssue1173HintMentionsCategory(t *testing.T) {
	s := newRedundantReverifyState()
	s.recordToolCall("run_command", "go test ./...", 1, false)
	hint := s.recordToolCall("run_command", "go test ./...", 2, false)
	if !strings.Contains(hint, "test") {
		t.Fatalf("hint should mention the verification category: %s", hint)
	}
}
