package agent

import (
	"strings"
	"testing"
)

func TestScopeOvergeneralize_NarrowGrepFollowedByUniversalClaim(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	// Narrow grep in a single directory
	s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)

	// Universal claim after narrow search
	hint := a.maybeWarnScopeOvergeneralize("There are no other callers of this function anywhere in the codebase.")
	if hint == "" {
		t.Fatal("expected warning for universal claim after narrow evidence")
	}
	if !strings.Contains(hint, "scope-overgeneralization") {
		t.Errorf("hint should contain detector name, got: %s", hint)
	}
	if !strings.Contains(hint, "narrow-scope") {
		t.Errorf("hint should mention narrow-scope, got: %s", hint)
	}
}

func TestScopeOvergeneralize_ReadFileThenOnlyClaim(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	// Single read_file (narrow)
	s.recordToolCall("read_file", `{"path": "internal/agent/agent.go"}`)

	hint := a.maybeWarnScopeOvergeneralize("These are the only files that need to be changed.")
	if hint == "" {
		t.Fatal("expected warning for 'only files' claim after single read")
	}
}

func TestScopeOvergeneralize_BroadGrepNoWarning(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	// Broad recursive grep
	s.recordToolCall("grep", `{"path": "**/*", "pattern": "MyFunc"}`)
	// Or repo-root grep
	s.recordToolCall("grep", `{"path": ".", "pattern": "MyFunc"}`)

	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint != "" {
		t.Errorf("should NOT warn when broad evidence was used, got: %s", hint)
	}
}

func TestScopeOvergeneralize_LSPReferencesNoWarning(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	// LSP references are definitive cross-reference tools
	s.recordToolCall("lsp_references", `{"path": "main.go", "line": 10}`)

	hint := a.maybeWarnScopeOvergeneralize("There are no other callers of this function.")
	if hint != "" {
		t.Errorf("should NOT warn when LSP evidence was used, got: %s", hint)
	}
}

func TestScopeOvergeneralize_NoClaimNoWarning(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)

	hint := a.maybeWarnScopeOvergeneralize("Found 3 matches in this directory.")
	if hint != "" {
		t.Errorf("should NOT warn when no universal claim present, got: %s", hint)
	}
}

func TestScopeOvergeneralize_NoEvidenceNoWarning(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}

	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint != "" {
		t.Errorf("should NOT warn without evidence, got: %s", hint)
	}
}

func TestScopeOvergeneralize_VerificationClearsState(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)
	// Run build/test
	s.recordToolCall("run_command", "go build ./...")
	// Now universal claim should not trigger (state was cleared)
	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint != "" {
		t.Errorf("should NOT warn after verification clears state, got: %s", hint)
	}
}

func TestScopeOvergeneralize_MaxWarnings(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	for i := 0; i < scopeOvergenMaxWarnings; i++ {
		s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)
		hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
		if hint == "" {
			t.Fatalf("expected warning on iteration %d", i)
		}
	}

	// Third call should be suppressed
	s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)
	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint != "" {
		t.Errorf("should suppress after max warnings, got: %s", hint)
	}
}

func TestScopeOvergeneralize_WindowExpiry(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	// Narrow evidence
	s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)

	// Fill window with non-evidence calls to push narrow evidence out
	for i := 0; i < scopeOvergenWindow+1; i++ {
		s.recordToolCall("edit_file", `{"path": "test.go"}`)
	}

	// Window expired, should not warn
	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint != "" {
		t.Errorf("should NOT warn after evidence expired from window, got: %s", hint)
	}
}

func TestScopeOvergeneralize_ResetClearsState(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	s.recordToolCall("grep", `{"path": "internal/agent", "pattern": "MyFunc"}`)
	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint == "" {
		t.Fatal("expected warning before reset")
	}

	s.reset()
	if s.warnings != 0 || s.narrowEvidenceCalls != 0 || s.hasRecentNarrow {
		t.Error("reset should clear all state")
	}
}

func TestScopeOvergeneralize_AllReferencesUpdatedClaim(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	// Narrow search
	s.recordToolCall("search_files", `{"directory": "internal/agent", "pattern": "oldFunc"}`)

	hint := a.maybeWarnScopeOvergeneralize("All references have been updated to the new function name.")
	if hint == "" {
		t.Fatal("expected warning for 'all references updated' claim after narrow search")
	}
}

func TestScopeOvergeneralize_OnlyTheseFilesClaim(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	s.recordToolCall("glob", `{"pattern": "internal/agent/*.go"}`)

	hint := a.maybeWarnScopeOvergeneralize("Only these 2 files are affected by this change.")
	if hint == "" {
		t.Fatal("expected warning for 'only these 2 files' claim after narrow glob")
	}
}

func TestScopeOvergeneralize_NothingElseNeededClaim(t *testing.T) {
	a := &Agent{scopeOvergeneralize: newScopeOvergeneralizeState()}
	s := a.scopeOvergeneralize

	s.recordToolCall("grep", `{"path": "internal/config", "pattern": "myKey"}`)

	hint := a.maybeWarnScopeOvergeneralize("Nothing else needs to be changed.")
	if hint == "" {
		t.Fatal("expected warning for 'nothing else needed' claim")
	}
}

func TestScopeOvergeneralize_NilStateSafe(t *testing.T) {
	a := &Agent{}
	hint := a.maybeWarnScopeOvergeneralize("There are no other callers anywhere.")
	if hint != "" {
		t.Error("nil state should return empty")
	}
}
