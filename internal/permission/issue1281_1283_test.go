package permission

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandSignatureDestructiveVariants(t *testing.T) {

	plain := commandSignature("git push origin main")
	for _, variant := range []string{
		"git push origin master --force",
		"git push origin --delete main",
		"git push origin :main",
		"git checkout main --force",
		"rm -rf /tmp/x",
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

	if commandSignature("git status") != commandSignature("git status --short") {
		t.Fatalf("harmless flag variants should still group")
	}
	if commandSignature("go build ./...") != commandSignature("go build ./internal/...") {
		t.Fatalf("go build variants should still group on the two-token key")
	}
}

func TestMakeKeyMultiFileSignsAllPaths(t *testing.T) {

	input := json.RawMessage(`{"files":[{"path":"/w/a/x.go","content":"x"},{"path":"/w/b/y.go","content":"y"}]}`)
	key1, ok := MakeKey("multi_file_write", input)
	if !ok || !strings.Contains(key1, "/b/") {
		t.Fatalf("#1281: second file's dir must be part of the key, got %q", key1)
	}

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

	am.EnsureModeScope(BypassMode)
	if am.ShouldAutoApprove("edit_file", input) {
		t.Fatal("approvals must not survive a permission mode switch")
	}
}

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

func TestIMToolActionSplit(t *testing.T) {
	p := newTestConfigPolicy(t)

	for _, action := range []string{"status", "mute", "unmute", "enable", "disable"} {
		d, err := p.Check("im", json.RawMessage(`{"action":"`+action+`"}`))
		if err != nil || d != Allow {
			t.Fatalf("#1283: benign im action %q must stay auto-allowed, got %v err=%v", action, d, err)
		}
	}

	p2 := newTestConfigPolicy(t)
	p2.SetOverride("im", Deny)
	d, err := p2.Check("im", json.RawMessage(`{"action":"send_file","path":"/w/secret.png"}`))
	if err != nil || d != Deny {
		t.Fatalf("#1283: user deny rule must now apply to im send_file, got %v err=%v", d, err)
	}
}

func newTestConfigPolicy(t *testing.T) *ConfigPolicy {
	t.Helper()
	return NewConfigPolicyWithMode(nil, []string{"/w"}, SupervisedMode)
}

func TestPowerShellQuoteEscaping(t *testing.T) {

	title := "x'; Remove-Item C:\\; $n.BalloonTipText='"
	escaped := strings.ReplaceAll(title, "'", "''")
	if strings.Contains(strings.Replace(escaped, "''", "", -1), "'") {
		t.Fatal("#1282: escaping must eliminate all unpaired quotes")
	}
}
