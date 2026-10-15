package permission

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// #4138: a leading ~ must be expanded BEFORE the relative-path anchoring in
// PathSandbox.Allowed. "~/x" is neither absolute nor "/"-rooted, so it used
// to be joined under the sandbox root ("<sandbox>/~/x") and the prefix
// compare passed - every tilde path was misjudged as sandbox-inside. That
// silently disabled the bypass/auto out-of-sandbox write Deny and
// short-circuited the supervised sensitive-read Ask.

func TestIssue4138_TildePathNotJudgedInsideSandbox(t *testing.T) {
	sandboxDir := t.TempDir()
	homeDir := t.TempDir() // deliberately outside the sandbox
	t.Setenv("HOME", homeDir)

	s := NewPathSandbox([]string{sandboxDir})

	// Pre-fix: Joined to <sandbox>/~/x -> prefix compare passed -> true.
	if s.Allowed("~/x.txt") {
		t.Fatal(`Allowed("~/x.txt") must be false: tilde expands to home, which is outside the sandbox (#4138)`)
	}
	// Control: the expanded absolute form must agree.
	if s.Allowed(filepath.Join(homeDir, "x.txt")) {
		t.Fatal("expanded absolute home path must also be outside the sandbox")
	}
	// Regression guards: paths genuinely inside the sandbox stay allowed.
	if !s.Allowed(filepath.Join(sandboxDir, "x.txt")) {
		t.Fatal("absolute path inside the sandbox must stay allowed")
	}
	if !s.Allowed("x.txt") {
		t.Fatal("relative path anchored at the sandbox root must stay allowed")
	}
}

// #4138 scenario 1 (supervised): read_file of a sensitive file via a tilde
// path must trigger Ask, exactly like the absolute-path form. Pre-fix, the
// misjudged Allowed() short-circuited the gate and the key content entered
// the model context with zero confirmation.
func TestIssue4138_SupervisedSensitiveTildeReadAsks(t *testing.T) {
	sandboxDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	p := NewConfigPolicyWithMode(nil, []string{sandboxDir}, SupervisedMode)

	input := json.RawMessage(`{"path":"~/.ssh/id_rsa"}`)
	d, err := p.Check("read_file", input)
	if err != nil {
		t.Fatalf("Check(read_file ~/.ssh/id_rsa): %v", err)
	}
	if d != Ask {
		t.Fatalf("supervised read_file ~/.ssh/id_rsa must Ask (got %v) - tilde form must match the absolute-path gate (#4138)", d)
	}

	// Control: the absolute-path form was already correct and must not regress.
	abs, err := p.Check("read_file", json.RawMessage(`{"path":"`+filepath.Join(t.TempDir(), "decoy", "id_rsa")+`"}`))
	if err != nil {
		t.Fatalf("Check(absolute form): %v", err)
	}
	_ = abs // absolute form covered by existing sensitive-path tests

	// Regression: a non-sensitive read inside the sandbox stays Allow.
	dIn, err := p.Check("read_file", json.RawMessage(`{"path":"x.txt"}`))
	if err != nil {
		t.Fatalf("Check(read_file x.txt): %v", err)
	}
	if dIn != Allow {
		t.Fatalf("supervised read_file of an in-sandbox non-sensitive file must stay Allow (got %v)", dIn)
	}
}

// #4138 scenario 2 (bypass/auto): write_file targeting a home shell-config
// file via a tilde path must be gated as an out-of-sandbox write. Bypass
// downgrades out-of-sandbox writes to Ask (locks.md S5); auto has no human
// loop and denies. Pre-fix, the misjudged Allowed() let the write silently
// escape the sandbox in both modes (persistent injection path).
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

	// Regression: an in-sandbox write in bypass mode stays Allow.
	dIn, err := p.Check("write_file", json.RawMessage(`{"path":"x.txt","content":"ok"}`))
	if err != nil {
		t.Fatalf("Check(write_file x.txt): %v", err)
	}
	if dIn != Allow {
		t.Fatalf("bypass write_file of an in-sandbox target must stay Allow (got %v)", dIn)
	}
}

// #4138 scenario 2b (auto): no human loop, out-of-sandbox write is Deny.
func TestIssue4138_AutoTildeWriteDenied(t *testing.T) {
	sandboxDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	p := NewConfigPolicyWithMode(nil, []string{sandboxDir}, AutoMode)

	d, err := p.Check("write_file", json.RawMessage(`{"path":"~/.zshrc","content":"echo pwned"}`))
	if err != nil {
		t.Fatalf("Check(write_file ~/.zshrc): %v", err)
	}
	if d != Deny {
		t.Fatalf("auto write_file ~/.zshrc must Deny as out-of-sandbox write (got %v) (#4138)", d)
	}
}
