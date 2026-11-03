package permission

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestIssue4138_TildePathNotJudgedInsideSandbox(t *testing.T) {
	sandboxDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	s := NewPathSandbox([]string{sandboxDir})

	if s.Allowed("~/x.txt") {
		t.Fatal(`Allowed("~/x.txt") must be false: tilde expands to home, which is outside the sandbox (#4138)`)
	}

	if s.Allowed(filepath.Join(homeDir, "x.txt")) {
		t.Fatal("expanded absolute home path must also be outside the sandbox")
	}

	if !s.Allowed(filepath.Join(sandboxDir, "x.txt")) {
		t.Fatal("absolute path inside the sandbox must stay allowed")
	}
	if !s.Allowed("x.txt") {
		t.Fatal("relative path anchored at the sandbox root must stay allowed")
	}
}

func TestIssue4138_SupervisedSensitiveTildeReadAsks(t *testing.T) {
	sandboxDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	p := NewConfigPolicyWithMode(nil, []string{sandboxDir}, SupervisedMode)

	input := json.RawMessage(`{"path":"~/.ssh/id_rsa"}`)
	d, err := p.Check("read_file", input)
	if err != nil {
		t.Fatalf("Check(read_file ~/.ssh/id_rsa): %v", err)
	}

	if d != Allow {
		t.Fatalf("supervised read_file ~/.ssh/id_rsa must Allow (two-mode refactor), got %v", d)
	}

	abs, err := p.Check("read_file", json.RawMessage(`{"path":"`+filepath.Join(t.TempDir(), "decoy", "id_rsa")+`"}`))
	if err != nil {
		t.Fatalf("Check(absolute form): %v", err)
	}
	_ = abs

	dIn, err := p.Check("read_file", json.RawMessage(`{"path":"x.txt"}`))
	if err != nil {
		t.Fatalf("Check(read_file x.txt): %v", err)
	}
	if dIn != Allow {
		t.Fatalf("supervised read_file of an in-sandbox non-sensitive file must stay Allow (got %v)", dIn)
	}
}

func TestIssue4138_BypassTildeWriteAsks(t *testing.T) {
	sandboxDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	p := NewConfigPolicyWithMode(nil, []string{sandboxDir}, BypassMode)

	input := json.RawMessage(`{"path":"~/.zshrc","content":"echo pwned"}`)
	d, err := p.Check("write_file", input)
	if err != nil {
		t.Fatalf("Check(write_file ~/.zshrc): %v", err)
	}
	if d != Ask {
		t.Fatalf("bypass write_file ~/.zshrc must gate as out-of-sandbox write (got %v) (#4138)", d)
	}

	dIn, err := p.Check("write_file", json.RawMessage(`{"path":"x.txt","content":"ok"}`))
	if err != nil {
		t.Fatalf("Check(write_file x.txt): %v", err)
	}
	if dIn != Allow {
		t.Fatalf("bypass write_file of an in-sandbox target must stay Allow (got %v)", dIn)
	}
}

func TestIssue4138_AutoTildeWriteDenied(t *testing.T) {
	sandboxDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	p := NewConfigPolicyWithMode(nil, []string{sandboxDir}, AutoMode)

	d, err := p.Check("write_file", json.RawMessage(`{"path":"~/.zshrc","content":"echo pwned"}`))
	if err != nil {
		t.Fatalf("Check(write_file ~/.zshrc): %v", err)
	}

	if d != Ask {
		t.Fatalf("auto(alias=supervised) write_file ~/.zshrc must Ask as out-of-sandbox write (got %v) (#4138)", d)
	}
}
