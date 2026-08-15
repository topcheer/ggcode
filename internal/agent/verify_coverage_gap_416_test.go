package agent

import (
	"strings"
	"testing"
)

// #416: absolute-path edits must match relative verify scopes — a fully
// verified package was warned UNVERIFIED in the most common production
// workflow (edit_file with absolute paths, go test with relative ./scope).
func TestCoverageAbsolutePathMatchesRelativeScope(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	// Verifies agent — the agent package itself must NOT be warned.
	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if warn == "" {
		t.Fatal("expected gap warning for internal/config")
	}
	if !strings.Contains(warn, "internal/config") {
		t.Errorf("warning should mention internal/config, got: %s", warn)
	}
	if !s.verifiedPkgs["/workspace/internal/agent"] {
		t.Error("agent package should now be marked verified")
	}

	// Sequential verification of the remaining package is a legitimate
	// strategy — no re-warning after config is verified too (#417).
	if warn2 := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/config/"})); warn2 != "" {
		t.Errorf("sequential per-package verification must not re-warn, got: %s", warn2)
	}
	if len(s.verifiedPkgs) != 2 {
		t.Errorf("expected 2 verified packages, got %d", len(s.verifiedPkgs))
	}
}

// #417: multi-path verify commands cover ALL listed ./paths.
func TestCoverageMultiPathScope(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/agent/a.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/config/b.go"}))

	if warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/ ./internal/config/"})); warn != "" {
		t.Errorf("multi-path command covers both packages, expected no warning, got: %s", warn)
	}

	scopes := coverageExtractVerifyScopes("go build ./internal/agent/ ./internal/config/")
	if len(scopes) != 2 || scopes[0] != "internal/agent" || scopes[1] != "internal/config" {
		t.Errorf("expected both scopes extracted, got: %v", scopes)
	}
}

// #417: bare `go test` in a package directory verifies that package (cwd
// proxied by the most recently edited package) instead of covering nothing.
func TestCoverageBareGoTestCoversLastEditedPkg(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/agent/a.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/config/b.go"}))

	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test"}))
	if warn == "" {
		t.Fatal("expected gap warning — only the last-edited package is covered by bare go test")
	}
	// Last edit was internal/config/b.go, so bare `go test` covers config;
	// internal/agent remains uncovered and should be the one listed.
	if !strings.Contains(warn, "internal/agent") {
		t.Errorf("warning should list uncovered internal/agent, got: %s", warn)
	}
	if strings.Contains(warn, "internal/config") {
		t.Errorf("last-edited package should be covered, not warned: %s", warn)
	}
	if !s.verifiedPkgs["internal/config"] {
		t.Error("last-edited package should be marked verified by bare go test")
	}
}
