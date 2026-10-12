package agent

// #3759: two classifier blind spots in verify_scope_narrow.
// A) the FULL import-path spelling of a package
//    (go test github.com/topcheer/ggcode/internal/agent) matched no
//    goTestPkgRe branch, so the most common cross-package narrowing
//    chain was classified scope:broad end-to-end and never fired.
// B) -run=TestX (flag equals form) produced no run: token (pytest -k=
//    and --grep= likewise).

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
	// The issue's repro: ./... fail -> full import path fail -> add -run
	// pass. Pre-fix, the middle step was scope:broad, so no narrowing
	// edge existed anywhere in the chain and the detector stayed silent.
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
	// -run=TestX must produce a run: token (pre-fix: silently none).
	if _, scope := classifyVerificationCommand("go test -run=TestFoo ./internal/agent/"); !strings.Contains(scope, "run:TestFoo") {
		t.Errorf("-run=TestX should yield run:TestFoo, got %q", scope)
	}
	// Space form keeps working.
	if _, scope := classifyVerificationCommand("go test -run TestFoo ./internal/agent/"); !strings.Contains(scope, "run:TestFoo") {
		t.Errorf("-run TestX should still yield run:TestFoo, got %q", scope)
	}
	// pytest -k= likewise (its token prefix is k:).
	if _, scope := classifyVerificationCommand("pytest -k=parser tests/"); !strings.Contains(scope, "k:parser") {
		t.Errorf("pytest -k=parser should yield k:parser, got %q", scope)
	}
	// npm --grep= likewise (its token prefix is grep:).
	if _, scope := classifyVerificationCommand("npm test --grep=auth"); !strings.Contains(scope, "grep:auth") {
		t.Errorf("npm test --grep=auth should yield grep:auth, got %q", scope)
	}
}

func TestIssue3759_DomainBranchNoFalsePositives(t *testing.T) {
	// Version tokens ("go1.27.1") have digit TLDs and must not match the
	// domain branch; a bare "go test" with only flags stays broad.
	if _, scope := classifyVerificationCommand("go test"); !strings.Contains(scope, "scope:broad") {
		t.Errorf("bare go test must stay broad, got %q", scope)
	}
	// go vet with a full import path is a narrow go-test category too.
	if _, scope := classifyVerificationCommand("go vet github.com/topcheer/ggcode/internal/mcp"); strings.Contains(scope, "scope:broad") {
		t.Errorf("go vet with full import path must not be broad, got %q", scope)
	}
}
