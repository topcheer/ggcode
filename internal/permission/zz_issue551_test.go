package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestIssue551D_ExitPlanModeRequiresConfirmation verifies that in Plan
// mode, exit_plan_mode returns Ask (user confirmation) instead of an
// unconditional Allow (#551-D). The comment above the branch has always
// required confirmation; the implementation contradicted it, letting a
// plan-mode agent exit on its own and silently regain write tools.
func TestIssue551D_ExitPlanModeRequiresConfirmation(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{t.TempDir()}, PlanMode)

	d, err := p.Check("exit_plan_mode", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Ask {
		t.Fatalf("exit_plan_mode in plan mode: got %v, want Ask (user confirmation)", d)
	}

	// Guard rails: entering plan mode stays allowed, read-only tools stay
	// allowed, and write tools stay denied in plan mode.
	if d, _ := p.Check("enter_plan_mode", json.RawMessage(`{}`)); d != Allow {
		t.Fatalf("enter_plan_mode in plan mode: got %v, want Allow", d)
	}
	if d, _ := p.Check("read_file", json.RawMessage(`{"path":"/tmp/x"}`)); d != Allow {
		t.Fatalf("read_file in plan mode: got %v, want Allow", d)
	}
	if d, _ := p.Check("write_file", json.RawMessage(`{"path":"/tmp/x"}`)); d != Deny {
		t.Fatalf("write_file in plan mode: got %v, want Deny", d)
	}
}

// TestIssue551E_GitCheckoutRefForm verifies that `git checkout <ref> -- <paths>`
// (e.g. `git checkout HEAD -- .` / `git checkout main -- src/`) is flagged
// dangerous (#551-E). Existing patterns only matched `git checkout -- .` and
// `git checkout .`, so the ref form discarded working tree changes without
// any warning.
func TestIssue551E_GitCheckoutRefForm(t *testing.T) {
	det := NewDangerousDetector()

	dangerous := []string{
		"git checkout HEAD -- .",
		"git checkout HEAD -- src/",
		"git checkout main -- src/",
		"git checkout v1.2.3 -- config.yaml",
		"git checkout origin/main -- .",
		"GIT CHECKOUT HEAD -- .",
	}
	for _, cmd := range dangerous {
		if !det.IsDangerous(cmd) {
			t.Errorf("IsDangerous(%q) = false, want true", cmd)
		}
		check := det.Check(cmd)
		if check.Level < DangerHigh {
			t.Errorf("Check(%q).Level = %s, want >= high", cmd, check.Level)
		}
	}

	// Non-destructive checkout forms must stay unmatched.
	safe := []string{
		"git checkout main",      // branch switch only, no pathspec
		"git checkout -b feat/x", // create branch
		"git checkout abc1234",   // detached HEAD, no pathspec
		"git checkout --help",    // help flag
	}
	for _, cmd := range safe {
		if det.IsDangerous(cmd) {
			t.Errorf("IsDangerous(%q) = true, want false (false positive)", cmd)
		}
	}
}

// TestIssue551F_SandboxRelativePathAnchoredToAllowedDir verifies that a
// relative path is judged against the sandbox's allowed directory (the
// agent's WorkingDir since #542 tools resolve relative paths there), not
// the process CWD (#551-F). With WorkingDir != process CWD the old
// CWD-anchored resolution produced false Deny/Allow verdicts.
func TestIssue551F_SandboxRelativePathAnchoredToAllowedDir(t *testing.T) {
	workspace := t.TempDir()
	otherDir := t.TempDir() // stands in for the process CWD

	// Move the process CWD away from the workspace.
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	sb := NewPathSandbox([]string{workspace})

	// Relative path must resolve under the workspace, not otherDir.
	if !sb.Allowed("notes.txt") {
		t.Error("Allowed(\"notes.txt\") = false with workspace allowedDir, want true (relative path anchored at allowed dir, not process CWD)")
	}
	if !sb.Allowed(filepath.Join("sub", "dir", "file.go")) {
		t.Error("nested relative path denied; want allowed under workspace")
	}

	// Absolute paths keep their existing semantics.
	if !sb.Allowed(filepath.Join(workspace, "file.go")) {
		t.Error("absolute path inside workspace denied")
	}
	if sb.Allowed(filepath.Join(otherDir, "file.go")) {
		t.Error("absolute path outside workspace allowed")
	}
}
