package agent

import (
	"testing"
)

func TestIntegrationRecordAndCheck(t *testing.T) {
	s := newIntegrationState()

	// Record evidence from a grep result.
	s.recordToolEvidence("grep", "internal/agent/auth.go:42: func Authenticate(token string) error")

	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence to be recorded")
	}

	// Assistant text that references the file path and symbol.
	guidance := s.checkIntegration("Let me look at internal/agent/auth.go to fix the Authenticate function")
	if guidance != "" {
		t.Fatalf("expected no warning for good integration, got: %s", guidance)
	}
}

func TestIntegrationNotIntegratedWarning(t *testing.T) {
	s := newIntegrationState()

	s.recordToolEvidence("grep", "internal/config/loader.go:100: func LoadConfig")

	if len(s.pendingEvidence) == 0 {
		t.Fatal("expected pending evidence")
	}

	// Assistant text that doesn't reference any evidence tokens.
	guidance := s.checkIntegration("Now I will create a new file for the feature.")
	if guidance == "" {
		t.Fatal("expected integration warning, got empty")
	}

	if s.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warnings)
	}
}

func TestIntegrationMaxWarnings(t *testing.T) {
	s := newIntegrationState()

	for i := 0; i < 5; i++ {
		s.recordToolEvidence("read_file", "internal/agent/agent.go:50: func FooBar")
		s.checkIntegration("unrelated text that doesn't match")
	}

	if s.warnings != integrationMaxWarnings {
		t.Fatalf("expected %d warnings (cap), got %d", integrationMaxWarnings, s.warnings)
	}
}

func TestIntegrationReset(t *testing.T) {
	s := newIntegrationState()
	s.warnings = 2
	s.pendingEvidence = []string{"test.go"}
	s.pendingTool = "grep"

	s.reset()

	if s.warnings != 0 || len(s.pendingEvidence) != 0 || s.pendingTool != "" {
		t.Fatal("reset did not clear state")
	}
}

func TestIntegrationIgnoresNonInfoTools(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("edit_file", "some content with path.go")
	if len(s.pendingEvidence) != 0 {
		t.Fatal("should not record evidence for edit_file")
	}
}

func TestIntegrationEmptyContent(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "")
	if len(s.pendingEvidence) != 0 {
		t.Fatal("should not record evidence for empty content")
	}
}

func TestIntegrationNoPendingEvidence(t *testing.T) {
	s := newIntegrationState()
	guidance := s.checkIntegration("some text")
	if guidance != "" {
		t.Fatal("expected empty guidance when no pending evidence")
	}
}

func TestIntegrationEmptyAssistantText(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("grep", "internal/agent/auth.go:42: func Authenticate")
	guidance := s.checkIntegration("")
	if guidance != "" {
		t.Fatal("expected empty guidance for empty assistant text")
	}
}

func TestIntegrationNoiseFiltered(t *testing.T) {
	s := newIntegrationState()
	// Content with mostly noise tokens.
	s.recordToolEvidence("grep", "go.mod: package main\nimport foo\nreturn error")
	if len(s.pendingEvidence) != 0 {
		t.Fatalf("expected noise to be filtered, got: %v", s.pendingEvidence)
	}
}

func TestIntegrationLineRefIntegration(t *testing.T) {
	s := newIntegrationState()
	s.recordToolEvidence("lsp_diagnostics", "auth.go line 142: undefined variable\nconfig.go line 50: type mismatch")

	if len(s.pendingEvidence) < 2 {
		t.Fatalf("expected at least 2 evidence tokens, got %d", len(s.pendingEvidence))
	}

	// Text referencing line numbers should count as integration.
	guidance := s.checkIntegration("I see the issue at line 142, let me fix it")
	if guidance != "" {
		t.Fatalf("expected no warning for line ref integration, got: %s", guidance)
	}
}

func TestIntegrationPartialIntegrationOK(t *testing.T) {
	s := newIntegrationState()
	// Many evidence tokens but agent references at least 1/3.
	s.recordToolEvidence("read_file", "internal/agent/agent.go:42: func Run\ninternal/agent/loop.go:100: func Loop\ninternal/agent/util.go:50: func Helper")

	if len(s.pendingEvidence) < 2 {
		t.Fatalf("expected at least 2 evidence tokens, got %d", len(s.pendingEvidence))
	}

	// Reference at least one file path.
	guidance := s.checkIntegration("I'll modify internal/agent/agent.go first")
	if guidance != "" {
		t.Fatalf("expected no warning for partial integration meeting threshold, got: %s", guidance)
	}
}
