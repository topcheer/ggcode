package tmux

// Regression probes for #3107 (client.go):
//   output() used cmd.CombinedOutput, so a benign stderr warning (server
//   first-start probe, config diagnostics) polluted every caller's parse
//   input -- worst on pane creation, where the whole mixed output became
//   Pane.ID and left the pane uncontrollable (Capture/Kill/PaneExists all
//   targeted a garbage string). The fix separates the streams (stdout is
//   the only parse input; stderr is logged) and validates the %N shape on
//   the creation path.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testContext is a cancelled-at-cleanup context for the fake-binary probes.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// extractPaneID is the pure gate: only a %N-shaped line becomes the id.
func TestIssue3107_ExtractPaneID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "%3\n", "%3"},
		{"warning before id", "warning: server first start probe\n%12\n", "%12"},
		{"anchored: surrounding spaces rejected", "  %7  \n", ""},
		{"only warning", "warning: config diagnostic\n", ""},
		{"numeric-only garbage", "12345\n", ""},
		{"percent-only", "%\n", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		got, err := extractPaneID(c.in)
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: expected error, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Anchored lines with trailing content after the digits must NOT match.
func TestIssue3107_ExtractPaneIDRejectsSuffixNoise(t *testing.T) {
	for _, in := range []string{"%3 extra\n", "id=%3\n", "%3%4\n"} {
		if m, err := extractPaneID(in); err == nil {
			t.Errorf("noise line %q accepted as pane id %q", in, m)
		}
	}
}

// output() separation: with a fake tmux binary that writes a warning to
// stderr and the payload to stdout, output() must return ONLY the stdout
// payload.
func TestIssue3107_OutputSeparatesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-tmux")
	script := "#!/bin/sh\n" +
		"echo 'warning: benign server probe' >&2\n" +
		"echo '%42'\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	c := &Client{bin: fake}
	out, err := c.output(testContext(t), "split-window", "-P", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if out != "%42\n" {
		t.Fatalf("stdout polluted by stderr: %q", out)
	}
}

// Full creation-path probe: the mixed-stream scenario from the issue must
// yield the clean %N id, not the warning line.
func TestIssue3107_SplitWindowIDStaysClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-tmux")
	script := "#!/bin/sh\n" +
		"echo 'no server running on /tmp/tmux-501/default, creating' >&2\n" +
		"echo '%17'\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	c := &Client{bin: fake}
	pane, err := c.Split(testContext(t), SplitRequest{Workspace: dir})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if pane.ID != "%17" {
		t.Fatalf("pane id polluted: %q (pane uncontrollable)", pane.ID)
	}
}

// Failure path parity: the error must still carry diagnostic content from
// BOTH streams (the old CombinedOutput error folded everything in).
func TestIssue3107_FailureKeepsDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-tmux")
	script := "#!/bin/sh\n" +
		"echo 'stdout detail'\n" +
		"echo 'stderr detail' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	c := &Client{bin: fake}
	_, err := c.output(testContext(t), "kill-pane", "-t", "%9")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stdout detail") || !strings.Contains(msg, "stderr detail") {
		t.Fatalf("error lost diagnostics: %q", msg)
	}
}
