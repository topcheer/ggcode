package im

// #1560-B: /diff over IM returned git's stderr+usage as the diff body on
// non-zero exit with non-empty output (the TUI path fixed this shape in
// #909; the IM path was the un-synced parity copy).

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIssue1560GitDiffErrorNotStderrBody(t *testing.T) {
	b := &DaemonBridge{workingDir: t.TempDir()} // no repo: git diff fails
	out, err := b.GitDiff([]string{"HEAD~999"})
	if err == nil {
		t.Fatalf("expected error, got output %q", out)
	}
	if strings.Contains(out, "usage:") {
		t.Fatal("stderr/usage must not be returned as the diff body")
	}
}

func TestIssue1560GitDiffCleanTree(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	b := &DaemonBridge{workingDir: dir}
	out, err := b.GitDiff(nil)
	if err != nil {
		t.Fatalf("clean tree must not error: %v", err)
	}
	if !strings.Contains(out, "No changes.") {
		t.Fatalf("clean tree message, got %q", out)
	}
}
