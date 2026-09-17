//go:build darwin

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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

func TestRunCommand_SandboxDeniesHomeWrite(t *testing.T) {
	// NOTE: this package's TestMain redirects HOME to a scratch dir that
	// lives under TMPDIR (and is therefore sandbox-allowed). The denial
	// probe must target the REAL user home, so resolve it from the passwd
	// database instead of the environment.
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skipf("cannot resolve real home dir: %v", err)
	}
	probe := filepath.Join(u.HomeDir, "ggcode-sbx-probe.txt")
	t.Cleanup(func() { os.Remove(probe) })

	rc := RunCommand{WorkingDir: t.TempDir(), Sandbox: NewSandboxPolicy(true, nil, nil)}
	res, execErr := rc.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"command": "echo pwned > %s"}`, probe)))
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}
	if !res.IsError {
		t.Fatalf("home write must fail under sandbox, got: %s", res.Content)
	}
	if errors.Is(errors.New(res.Content), errSandboxUnavailable) || strings.Contains(res.Content, errSandboxUnavailable.Error()) {
		t.Skipf("sandbox-exec unavailable on this machine: %s", res.Content)
	}
	if !strings.Contains(res.Content, "sandbox") {
		t.Fatalf("denial must carry the sandbox hint, got: %s", res.Content)
	}
	if _, statErr := os.Stat(probe); statErr == nil {
		t.Fatal("probe file must not exist after denied write")
	}
}

func TestRunCommand_SandboxAllowsTmpWrite(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "out.txt")
	rc := RunCommand{WorkingDir: tmp, Sandbox: NewSandboxPolicy(true, nil, nil)}
	res, execErr := rc.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"command": "echo ok > %s"}`, out)))
	if execErr != nil {
		t.Fatalf("execute: %v", execErr)
	}
	if strings.Contains(res.Content, errSandboxUnavailable.Error()) {
		t.Skipf("sandbox-exec unavailable: %s", res.Content)
	}
	if res.IsError {
		t.Fatalf("temp write must succeed under sandbox, got: %s", res.Content)
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil || strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("allowed write produced %q (err=%v)", data, readErr)
	}
}
