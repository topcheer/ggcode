package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIssue551D_ExitPlanModeRestoresWithoutGate(t *testing.T) {
	p := NewConfigPolicyWithMode(nil, []string{t.TempDir()}, PlanMode)

	d, err := p.Check("exit_plan_mode", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if d != Allow {
		t.Fatalf("exit_plan_mode in plan mode: got %v, want Allow (restores the user's own previous mode)", d)
	}

	if d, _ := p.Check("switch_mode", json.RawMessage(`{"mode":"bypass"}`)); d != Ask {
		t.Fatalf("switch_mode→bypass in plan mode: got %v, want Ask (#1705)", d)
	}

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

	safe := []string{
		"git checkout main",
		"git checkout -b feat/x",
		"git checkout abc1234",
		"git checkout --help",
	}
	for _, cmd := range safe {
		if det.IsDangerous(cmd) {
			t.Errorf("IsDangerous(%q) = true, want false (false positive)", cmd)
		}
	}
}

func TestIssue551F_SandboxRelativePathAnchoredToAllowedDir(t *testing.T) {
	workspace := t.TempDir()
	otherDir := t.TempDir()

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	sb := NewPathSandbox([]string{workspace})

	if !sb.Allowed("notes.txt") {
		t.Error("Allowed(\"notes.txt\") = false with workspace allowedDir, want true (relative path anchored at allowed dir, not process CWD)")
	}
	if !sb.Allowed(filepath.Join("sub", "dir", "file.go")) {
		t.Error("nested relative path denied; want allowed under workspace")
	}

	if !sb.Allowed(filepath.Join(workspace, "file.go")) {
		t.Error("absolute path inside workspace denied")
	}
	if sb.Allowed(filepath.Join(otherDir, "file.go")) {
		t.Error("absolute path outside workspace allowed")
	}
}
