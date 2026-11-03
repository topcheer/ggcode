package agent

import (
	"strings"
	"testing"
)

func TestIssue3759_FullImportPathIsNarrowScope(t *testing.T) {
	cat, scope := classifyVerificationCommand("go test github.com/topcheer/ggcode/internal/agent")
	if cat != "go-test" {
		t.Fatalf("category = %q, want go-test", cat)
	}
	if strings.Contains(scope, "scope:broad") {
		t.Fatalf("full import path must not be classified broad, got scope %q", scope)
	}
	if !strings.Contains(scope, "pkg:github.com/topcheer/ggcode/internal/agent") {
		t.Fatalf("scope should carry the import path as pkg:, got %q", scope)
	}
}

func TestIssue3759_FullImportPathNarrowingChainFires(t *testing.T) {
	s := newScopeNarrowState()

	if msg := s.recordVerificationCommand("run_command", "go test ./...",
		"FAIL  pkg1 [build failed]\nFAIL", true); msg != "" {
		t.Fatalf("first call should not fire, got %q", msg)
	}
	if msg := s.recordVerificationCommand("run_command", "go test github.com/topcheer/ggcode/internal/agent",
		"FAIL  TestFoo [run failed]", true); msg != "" {
		t.Fatalf("second call should not fire, got %q", msg)
	}
	msg := s.recordVerificationCommand("run_command", "go test -run=TestBar github.com/topcheer/ggcode/internal/agent",
		"ok  internal/agent/", false)
	if msg == "" {
		t.Fatal("narrowing chain over full import paths must fire the warning")
	}
	if !strings.Contains(msg, "Verification Scope Narrowing") {
		t.Errorf("warning should mention scope narrowing, got: %q", msg)
	}
}

func TestIssue3759_EqualsFormFilters(t *testing.T) {

	if _, scope := classifyVerificationCommand("go test -run=TestFoo ./internal/agent/"); !strings.Contains(scope, "run:TestFoo") {
		t.Errorf("-run=TestX should yield run:TestFoo, got %q", scope)
	}

	if _, scope := classifyVerificationCommand("go test -run TestFoo ./internal/agent/"); !strings.Contains(scope, "run:TestFoo") {
		t.Errorf("-run TestX should still yield run:TestFoo, got %q", scope)
	}

	if _, scope := classifyVerificationCommand("pytest -k=parser tests/"); !strings.Contains(scope, "k:parser") {
		t.Errorf("pytest -k=parser should yield k:parser, got %q", scope)
	}

	if _, scope := classifyVerificationCommand("npm test --grep=auth"); !strings.Contains(scope, "grep:auth") {
		t.Errorf("npm test --grep=auth should yield grep:auth, got %q", scope)
	}
}

func TestIssue3759_DomainBranchNoFalsePositives(t *testing.T) {

	if _, scope := classifyVerificationCommand("go test"); !strings.Contains(scope, "scope:broad") {
		t.Errorf("bare go test must stay broad, got %q", scope)
	}

	if _, scope := classifyVerificationCommand("go vet github.com/topcheer/ggcode/internal/mcp"); strings.Contains(scope, "scope:broad") {
		t.Errorf("go vet with full import path must not be broad, got %q", scope)
	}
}
