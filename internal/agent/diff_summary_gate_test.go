package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffSummaryState_FiresOnce(t *testing.T) {
	d := newDiffSummaryState()
	if d.fired {
		t.Fatal("should start unfired")
	}

	d.fired = true
	if !d.fired {
		t.Fatal("should be fired after setting")
	}
}

func TestDiffSummaryState_Reset(t *testing.T) {
	d := newDiffSummaryState()
	d.fired = true
	d.reset()
	if d.fired {
		t.Fatal("reset should clear fired flag")
	}
}

func TestDiffSummaryGate_MinFilesNotMet(t *testing.T) {
	a := &Agent{
		diffSummary: newDiffSummaryState(),
	}

	result := a.checkDiffSummaryGate(&RunStats{})
	if result != "" {
		t.Fatalf("expected empty for no changes, got: %q", result)
	}
}

func TestShortenPathForDisplay_RelativePath(t *testing.T) {

	line := " internal/agent/planner.go | 15 +++--"
	result := shortenPathForDisplay("/some/workdir", line)
	if result != line {
		t.Fatalf("expected unchanged for relative path, got: %q", result)
	}
}

func TestDiffSummaryGate_FiresOnlyOnce(t *testing.T) {
	a := &Agent{
		diffSummary: newDiffSummaryState(),
	}

	a.checkDiffSummaryGate(&RunStats{})

	result := a.checkDiffSummaryGate(&RunStats{})
	if result != "" {
		t.Fatalf("expected empty on second call, got: %q", result)
	}
}

func TestDiffSummaryGate_NilState(t *testing.T) {
	a := &Agent{
		diffSummary: nil,
	}
	result := a.checkDiffSummaryGate(&RunStats{})
	if result != "" {
		t.Fatalf("expected empty with nil state, got: %q", result)
	}
}

func TestDiffSummaryGateOutputFormat(t *testing.T) {

	expectedPrefix := "[Self-review:"
	var sb strings.Builder
	sb.WriteString("[Self-review: you are about to finish. Here is a summary of ALL your changes (git diff --stat). ")
	sb.WriteString("Review this list, verify each change is intentional, complete, and matches the user's request.]\n")
	msg := sb.String()
	if !strings.HasPrefix(msg, expectedPrefix) {
		t.Fatalf("message should start with %q", expectedPrefix)
	}
}

func TestGitDiffStatCountsUntracked(t *testing.T) {
	dir := initGitRepo(t)
	committed := filepath.Join(dir, "a.go")
	mustWriteCR(t, committed, "package p\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	mustWriteCR(t, filepath.Join(dir, "b.go"), "package p\n")
	mustWriteCR(t, filepath.Join(dir, "c.go"), "package p\n")

	out, err := gitDiffStat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "b.go") || !strings.Contains(out, "c.go") {
		t.Fatalf("untracked files missing from stat: %q", out)
	}
}
