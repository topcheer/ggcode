package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssue4787_SpawnEnoentClassifier(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want bool
	}{
		{"verbatim S-16", "fork/exec /bin/sh: no such file or directory", true},
		{"darwin sh3", "fork/exec /bin/sh: no such file or directory (S-16 wave)", true},
		{"job text", "failed to start command: fork/exec /bin/sh: no such file or directory", true},
		{"chdir is NOT spawn-enoent", "chdir /gone/dir: no such file or directory", false},
		{"inner command not found is NOT spawn-enoent", "sh: foo: command not found", false},
		{"plain exec error", "exit status 1", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := isSpawnEnoentFailureText(tc.s); got != tc.want {
			t.Errorf("%s: isSpawnEnoentFailureText(%q) = %v, want %v", tc.name, tc.s, got, tc.want)
		}
	}
}

func TestIssue4787_ShellDegradedHintMentionsProbe(t *testing.T) {
	h := shellDegradedHint()
	if !strings.Contains(h, "[SHELL-DEGRADED]") {
		t.Fatalf("hint must carry the SHELL-DEGRADED signal marker: %q", h)
	}
	if !strings.Contains(h, "ggcode-shell-health-probe-ok") {
		t.Fatalf("hint must direct the agent to the health probe command: %q", h)
	}
	if !strings.Contains(h, "Do NOT change strategy") {
		t.Fatalf("hint must tell the agent not to pivot strategy: %q", h)
	}
}

func TestIssue4787_RunCommandRecoversAfterWorkdirRemoved(t *testing.T) {
	root := t.TempDir()
	dead := filepath.Join(root, "worktree-abc")
	if err := os.MkdirAll(dead, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := RunCommand{WorkingDir: dead}

	input := json.RawMessage(`{"command": "pwd"}`)
	res, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("healthy run: %v", err)
	}
	if res.IsError {
		t.Fatalf("healthy run should succeed, got: %s", res.Content)
	}

	if err := os.RemoveAll(dead); err != nil {
		t.Fatal(err)
	}

	res2, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("post-removal run: %v", err)
	}
	if res2.IsError {
		t.Fatalf("post-removal run must recover at the nearest live ancestor, got error: %s", res2.Content)
	}
	if !strings.Contains(res2.Content, filepath.Base(root)) {
		t.Fatalf("recovered run should execute at the live ancestor, output: %q", res2.Content)
	}
}

func TestIssue4787_HealthProbeCommandRuns(t *testing.T) {
	rc := RunCommand{WorkingDir: t.TempDir()}
	input := json.RawMessage(`{"command": "echo ggcode-shell-health-probe-ok"}`)
	res, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("probe should succeed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "ggcode-shell-health-probe-ok") {
		t.Fatalf("probe output missing marker: %q", res.Content)
	}
}

func TestIssue4787_NilErrorIsNotSpawnFailure(t *testing.T) {
	if isSpawnEnoentFailure(nil) {
		t.Fatal("nil error must not be a spawn failure")
	}
}

func TestIssue4787_BackoffBounded(t *testing.T) {
	if shellOutageBackoff > 5*time.Second {
		t.Fatalf("shellOutageBackoff %v too long for an in-call retry", shellOutageBackoff)
	}
	if shellOutageBackoff <= 0 {
		t.Fatal("shellOutageBackoff must be positive")
	}
}
