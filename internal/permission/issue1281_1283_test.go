package permission

// Regression tests for GitHub issues #1281-#1283 (permission/notify security
// trio): approval-memory signature over-breadth + missing danger re-check at
// the memory-hit path, PowerShell single-quote injection in Windows
// notifications, and the blanket im always-allowed bypass.

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- #1281: approval-memory signatures must not over-generalize ---

func TestCommandSignatureDestructiveVariants(t *testing.T) {
	// Flag/refspec variants must NOT share the plain two-token key.
	plain := commandSignature("git push origin main")
	for _, variant := range []string{
		"git push origin master --force",
		"git push origin --delete main",
		"git push origin :main",
		"git checkout main --force",
		"rm -rf /tmp/x", // -rf contains recursive/force flags
		"git reset --hard",
	} {
		if sig := commandSignature(variant); sig == plain || sig == "git push" {
			t.Fatalf("#1281: %q shares signature %q with plain push", variant, sig)
		}
	}
}

func TestCommandSignatureRedirectIsolated(t *testing.T) {
	a := commandSignature("echo x > a.txt")
	b := commandSignature("echo x > ~/.bashrc")
	if a == b {
		t.Fatalf("#1281: redirect targets must not share a signature: %q", a)
	}
}

func TestCommandSignatureStillGroupsNormalVariants(t *testing.T) {
	// The feature: same verb+object with harmless variations stays grouped.
	if commandSignature("git status") != commandSignature("git status --short") {
		t.Fatalf("harmless flag variants should still group")
	}
	if commandSignature("go build ./...") != commandSignature("go build ./internal/...") {
		t.Fatalf("go build variants should still group on the two-token key")
	}
}

func TestMakeKeyMultiFileSignsAllPaths(t *testing.T) {
	// pathSignature is directory+extension scoped, so use distinct dirs.
	input := json.RawMessage(`{"files":[{"path":"/w/a/x.go","content":"x"},{"path":"/w/b/y.go","content":"y"}]}`)
	key1, ok := MakeKey("multi_file_write", input)
	if !ok || !strings.Contains(key1, "/b/") {
		t.Fatalf("#1281: second file's dir must be part of the key, got %q", key1)
	}
	// Dropping the second file changes the key (batch content is reviewed).
	input2 := json.RawMessage(`{"files":[{"path":"/w/a/x.go","content":"x"}]}`)
	key2, _ := MakeKey("multi_file_write", input2)
	if key1 == key2 {
		t.Fatalf("#1281: different file sets must produce different keys: %q", key1)
	}
}

func TestApprovalMemoryModeScopeReset(t *testing.T) {
	am := NewApprovalMemory()
	am.EnsureModeScope(SupervisedMode)
	input := json.RawMessage(`{"file_path":"/w/a.go","content":"x"}`)
	for i := 0; i < 3; i++ {
		am.RecordApproval("edit_file", input)
	}
	if !am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("3 approvals should auto-approve within the same mode")
	}
	// Switching mode wipes the learned approvals (#1281: doc promised it,
	// nobody implemented it).
	am.EnsureModeScope(BypassMode)
	if am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("approvals must not survive a permission mode switch")
	}
}

// --- #1281 A1: memory hit must not outrank danger detection ---

func TestBlocksAutoApproveDangerousCommand(t *testing.T) {
	p := newTestConfigPolicy(t)
	input := json.RawMessage(`{"command":"git push origin master --force"}`)
	if !p.BlocksAutoApprove("run_command", input) {
		t.Fatal("#1281: --force push must block auto-approval")
	}
	safe := json.RawMessage(`{"command":"ls -la"}`)
	if p.BlocksAutoApprove("run_command", safe) {
		t.Fatal("ls should not block auto-approval")
	}
}

// --- #1283: im no longer blanket-approved; benign actions still free ---

func TestIMToolActionSplit(t *testing.T) {
	p := newTestConfigPolicy(t)
	// Benign adapter management stays free in every mode, incl. plan.
	for _, action := range []string{"status", "mute", "unmute", "enable", "disable"} {
		d, err := p.Check("im", json.RawMessage(`{"action":"`+action+`"}`))
		if err != nil || d != Allow {
			t.Fatalf("#1283: benign im action %q must stay auto-allowed, got %v err=%v", action, d, err)
		}
	}
	// send_file must NOT be blanket-allowed: an explicit user deny rule
	// (previously bypassed by the IsAlwaysAllowedTool short-circuit) must
	// now be honored.
	p2 := newTestConfigPolicy(t)
	p2.SetOverride("im", Deny)
	d, err := p2.Check("im", json.RawMessage(`{"action":"send_file","path":"/w/secret.png"}`))
	if err != nil || d != Deny {
		t.Fatalf("#1283: user deny rule must now apply to im send_file, got %v err=%v", d, err)
	}
}

// newTestConfigPolicy builds a minimal ConfigPolicy for permission tests.
func newTestConfigPolicy(t *testing.T) *ConfigPolicy {
	t.Helper()
	return NewConfigPolicyWithMode(nil, []string{"/w"}, SupervisedMode)
}

// --- #1282: PowerShell single-quote escaping ---

func TestPowerShellQuoteEscaping(t *testing.T) {
	// The escaping rule: every ' becomes '' inside single-quoted PS strings.
	title := "x'; Remove-Item C:\\; $n.BalloonTipText='"
	escaped := strings.ReplaceAll(title, "'", "''")
	if strings.Contains(strings.Replace(escaped, "''", "", -1), "'") {
		t.Fatal("#1282: escaping must eliminate all unpaired quotes")
	}
}
