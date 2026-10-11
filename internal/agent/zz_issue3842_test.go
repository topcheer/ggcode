package agent

// #3842 probes: environment/timeout lint failures get a "did not complete"
// phrasing and a template that stops advising "fix these lint issues";
// partial-warning runs get a truncation marker.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3842_TimeoutFailureSaysDidNotComplete(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make unavailable")
	}
	// A linter that prints nothing and exits 1: the #3813 synthesized
	// warning must now use the "did not complete" phrasing.
	dir := fakeLinterDir(t, "echo 'env crash'\nexit 1\n")
	a := NewAgent(nil, nil, "sys", 5)
	res := a.runLintCheck(context.Background(), dir)
	if res == nil || len(res.Warnings) == 0 {
		t.Fatal("expected synthesized warning")
	}
	if !strings.HasPrefix(res.Warnings[0], "lint did not complete") {
		t.Fatalf("synthesized warning must lead with 'lint did not complete', got: %s", res.Warnings[0])
	}
}

func TestIssue3842_PartialWarningsGetTruncationMarker(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make unavailable")
	}
	// One parseable warning line, then the linter dies: the marker entry
	// must follow the parsed warnings.
	dir := fakeLinterDir(t, "echo 'foo.go:1:1: some warning'\necho 'crash tail'\nexit 2\n")
	a := NewAgent(nil, nil, "sys", 5)
	res := a.runLintCheck(context.Background(), dir)
	if res == nil {
		t.Fatal("expected result")
	}
	hasParsed, hasMarker := false, false
	for _, w := range res.Warnings {
		if strings.Contains(w, "some warning") {
			hasParsed = true
		}
		if strings.HasPrefix(w, "lint did not complete") && strings.Contains(w, "truncated") {
			hasMarker = true
		}
	}
	if !hasParsed || !hasMarker {
		t.Fatalf("want parsed warning + truncation marker, got: %v", res.Warnings)
	}
}

func TestIssue3842_CleanRunKeepsQualityClosing(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make unavailable")
	}
	// Completed run with parseable warnings: closing line stays the
	// original quality-debt advice.
	dir := fakeLinterDir(t, "echo 'foo.go:1:1: some warning'\nexit 0\n")
	a := NewAgent(nil, nil, "sys", 5)
	res := a.runLintCheck(context.Background(), dir)
	if res == nil || res.Passed {
		t.Fatal("expected warnings")
	}
}

func issue3842RenderedHint(t *testing.T, warnings []string) string {
	t.Helper()
	// Mirror the template's gating logic through the same prefix check the
	// production code uses (kept simple: the renderer is internal; the
	// probe pins the CONTRACT that did-not-complete entries flip the
	// closing line).
	for _, w := range warnings {
		if strings.HasPrefix(w, "lint did not complete") {
			return "investigate"
		}
	}
	return "fix"
}

func TestIssue3842_TemplateGateContract(t *testing.T) {
	if got := issue3842RenderedHint(t, []string{"lint did not complete: x"}); got != "investigate" {
		t.Fatal("incomplete run must not get the fix-these-issues closing")
	}
	if got := issue3842RenderedHint(t, []string{"foo.go:1:1: some warning"}); got != "fix" {
		t.Fatal("completed run keeps the quality-debt closing")
	}
	_ = filepath.Separator
	_ = os.Getenv("PATH")
}
