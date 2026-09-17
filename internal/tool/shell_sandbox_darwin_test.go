//go:build darwin

package tool

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestWrapShellCommandOS_DarwinRewrites(t *testing.T) {
	p := NewSandboxPolicy(true, nil, nil)
	workspace := t.TempDir()
	cmd := &exec.Cmd{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "echo hi"}}

	wrapped, err := wrapShellCommandOS(cmd, workspace, p)
	if err != nil {
		// sandbox-exec missing: the contract is fail-closed, not silently
		// unwrapped.
		if errors.Is(err, errSandboxUnavailable) {
			t.Skipf("sandbox-exec unavailable: %v", err)
		}
		t.Fatalf("wrap failed: %v", err)
	}
	if !wrapped {
		t.Fatal("enabled policy on darwin must wrap")
	}
	if cmd.Path != "sandbox-exec" && !strings.HasSuffix(cmd.Path, "/sandbox-exec") {
		t.Fatalf("cmd.Path must point at sandbox-exec, got %q", cmd.Path)
	}
	if len(cmd.Args) < 4 || cmd.Args[1] != "-p" {
		t.Fatalf("cmd.Args must be [bin, -p, profile, shell, ...], got %v", cmd.Args)
	}
	profile := cmd.Args[2]
	if !strings.Contains(profile, "(allow default)") || !strings.Contains(profile, "(deny file-write*)") {
		t.Fatalf("profile missing broad deny/allow shape:\n%s", profile)
	}
	if !strings.Contains(profile, workspace) {
		t.Fatalf("profile must allow the workspace %q:\n%s", workspace, profile)
	}
	if strings.Contains(profile, "(deny network*)") {
		t.Fatalf("default policy allows network, got deny:\n%s", profile)
	}
}

func TestSeatbeltProfile_NetworkDeny(t *testing.T) {
	denied := false
	profile, err := seatbeltProfile(t.TempDir(), NewSandboxPolicy(true, &denied, nil))
	if err != nil {
		t.Fatalf("seatbeltProfile: %v", err)
	}
	if !strings.Contains(profile, "(deny network*)") {
		t.Fatalf("AllowNetwork=false must emit network deny:\n%s", profile)
	}
}

func TestSeatbeltProfile_RejectsUnsafePath(t *testing.T) {
	profile, err := seatbeltProfile(t.TempDir(), NewSandboxPolicy(true, nil, []string{`bad"quote`}))
	if err == nil {
		t.Fatalf("unsafe path must fail closed, got profile:\n%s", profile)
	}
}
