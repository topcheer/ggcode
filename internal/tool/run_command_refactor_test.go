package tool

import (
	"context"
	"strings"
	"testing"
)

// TestResolveShellCommandTrailerInjection verifies that git commit commands
// are rewritten with the Co-Authored-By trailer before the shell is
// resolved, and that the returned final command reflects the rewrite.
func TestResolveShellCommandTrailerInjection(t *testing.T) {
	rc := RunCommand{}
	cmd, final, err := rc.resolveShellCommand(context.Background(), `git commit -m "test"`)
	if err != nil {
		t.Fatalf("resolveShellCommand failed: %v", err)
	}
	if !strings.Contains(final, "Co-Authored-By:") {
		t.Errorf("final command missing Co-Authored-By trailer: %q", final)
	}
	if cmd == nil || cmd.Path == "" {
		t.Errorf("expected resolved cmd, got %+v", cmd)
	}
}

// TestResolveShellCommandNoTrailerForNonCommit verifies plain commands pass
// through unmodified.
func TestResolveShellCommandNoTrailerForNonCommit(t *testing.T) {
	rc := RunCommand{}
	const want = "echo hello"
	_, final, err := rc.resolveShellCommand(context.Background(), want)
	if err != nil {
		t.Fatalf("resolveShellCommand failed: %v", err)
	}
	if final != want {
		t.Errorf("final command = %q, want %q", final, want)
	}
}

// TestResolveShellCommandEnvNormalization verifies the deterministic
// terminal environment and git pager suppression.
func TestResolveShellCommandEnvNormalization(t *testing.T) {
	rc := RunCommand{}
	cmd, _, err := rc.resolveShellCommand(context.Background(), "git status")
	if err != nil {
		t.Fatalf("resolveShellCommand failed: %v", err)
	}
	env := map[string]string{}
	for _, kv := range cmd.Env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	for _, key := range []string{"TERM", "NO_COLOR", "COLUMNS", "CI"} {
		if _, ok := env[key]; !ok {
			t.Errorf("env missing %s", key)
		}
	}
	if env["GIT_PAGER"] != "cat" {
		t.Errorf("GIT_PAGER = %q, want cat", env["GIT_PAGER"])
	}
}

// TestResolveShellCommandWorkingDirPinned verifies the agent's fixed
// WorkingDir wins and LLM-provided dirs are ignored.
func TestResolveShellCommandWorkingDirPinned(t *testing.T) {
	rc := RunCommand{WorkingDir: "/tmp"}
	cmd, _, err := rc.resolveShellCommand(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("resolveShellCommand failed: %v", err)
	}
	if cmd.Dir != "/tmp" {
		t.Errorf("cmd.Dir = %q, want /tmp", cmd.Dir)
	}
}

// TestCommandRunStructFieldsExist guards the shared runner struct so a
// future field rename cannot silently break the GUI/foreground dispatch.
func TestCommandRunStructFieldsExist(t *testing.T) {
	r := commandRun{command: "x", preWarning: "w", sandboxed: true}
	if r.command != "x" || r.preWarning != "w" || !r.sandboxed {
		t.Errorf("unexpected commandRun state: %+v", r)
	}
}
