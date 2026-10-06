package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// r26: stateful workflow-spec engine (Lean4Agent-inspired). Tests use the
// lazy loader anchored at a temp project dir with .ggcode/workflow-spec.json.

func newWFEngineWithSpec(t *testing.T, specJSON string) *workflowEngine {
	t.Helper()
	dir := t.TempDir()
	gg := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(gg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gg, workflowSpecFileName), []byte(specJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return &workflowEngine{loadDir: gg}
}

const spec3373family = `{
  "steps": [
    {"id":"write-tests","artifact_glob":"internal/**/*_test.go"},
    {"id":"run-tests","artifact_glob":"","requires":["write-tests"],"mode":"warn","on_commands":["go test*"]},
    {"id":"release","artifact_glob":"","requires":["run-tests"],"mode":"block","on_commands":["git push*","git tag*"]}
  ]
}`

var runGoTest = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}
var runGitPush = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"git push origin main"}`)}

// Guarded command whose prerequisite has no artifact yet: block-mode
// violation naming the counterexample step.
func TestWorkflowSpec_BlockNamesMissingPrerequisite(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3373family)
	v := e.checkPreconditions(runGitPush.Name, runGitPush.Arguments)
	if v == nil {
		t.Fatal("expected violation: release guarded, run-tests not complete")
	}
	if v.Missing != "run-tests" {
		t.Errorf("counterexample step = %q, want run-tests", v.Missing)
	}
	if v.Step.Mode != "block" {
		t.Errorf("mode = %q, want block", v.Step.Mode)
	}
}

// Artifact grounding completes a step and lifts the gate on dependents.
func TestWorkflowSpec_ArtifactGroundsCompletion(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3373family)
	e.recordCompletion("internal/agent/foo_test.go") // write-tests done
	if !e.isComplete("write-tests") {
		t.Fatal("artifact should ground write-tests")
	}
	// run-tests is now allowed (its only prerequisite is complete)...
	if v := e.checkPreconditions(runGoTest.Name, runGoTest.Arguments); v != nil {
		t.Fatalf("run-tests should pass after write-tests: %v", v.Step.ID)
	}
	// ...but release still requires run-tests, which has NO artifact_glob
	// (artifact ""), so it stays incomplete forever by design -> still blocked.
	if v := e.checkPreconditions(runGitPush.Name, runGitPush.Arguments); v == nil {
		t.Fatal("release must still be blocked (run-tests has no artifact to ground it)")
	}
}

// Steps without artifact_glob (pure gate nodes) are never "outstanding" -
// only artifact-backed steps appear in the end-of-run audit.
func TestWorkflowSpec_OutstandingOnlyCountsArtifactSteps(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3373family)
	msg := e.outstandingMessage()
	if !strings.Contains(msg, "write-tests") {
		t.Errorf("write-tests (artifact step) should be outstanding:\n%s", msg)
	}
	if strings.Contains(msg, "release") {
		t.Errorf("release (no artifact) must not appear as outstanding:\n%s", msg)
	}
	e.recordCompletion("internal/x/y_test.go")
	if msg := e.outstandingMessage(); msg != "" {
		t.Errorf("fully grounded spec must produce no message, got:\n%s", msg)
	}
}

// No spec file -> inert everywhere (default path unchanged).
func TestWorkflowSpec_InertWithoutFile(t *testing.T) {
	dir := t.TempDir()
	e := &workflowEngine{loadDir: filepath.Join(dir, ".ggcode")}
	if v := e.checkPreconditions(runGitPush.Name, runGitPush.Arguments); v != nil {
		t.Fatal("no spec = no violation")
	}
	if msg := e.outstandingMessage(); msg != "" {
		t.Fatalf("no spec = no message, got %q", msg)
	}
}

// Broken JSON degrades to inert (fail-open, mirroring invariants).
func TestWorkflowSpec_BrokenSpecDegrades(t *testing.T) {
	e := newWFEngineWithSpec(t, `{"steps": [BROKEN`)
	if v := e.checkPreconditions(runGitPush.Name, runGitPush.Arguments); v != nil {
		t.Fatal("broken spec must degrade to inert")
	}
}

// Non-run_command tools never trigger the gate.
func TestWorkflowSpec_OnlyGuardsRunCommand(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3373family)
	tc := provider.ToolCallDelta{Name: "write_file", Arguments: json.RawMessage(`{"path":"x.go"}`)}
	if v := e.checkPreconditions(tc.Name, tc.Arguments); v != nil {
		t.Fatal("write_file must not trigger the workflow gate")
	}
}

// Command glob semantics: prefix/suffix/infix/exact.
func TestWorkflowCommandMatches(t *testing.T) {
	cases := []struct {
		patterns []string
		cmd      string
		want     bool
	}{
		{[]string{"git push*"}, "git push origin main", true},
		{[]string{"git push*"}, "git commit -m x", false},
		{[]string{"*test*"}, "go test ./...", true},
		{[]string{"*make"}, "make", true}, // suffix on trimmed pattern
		{[]string{"go vet ./..."}, "go vet ./...", true},
		{[]string{"go vet ./..."}, "go vet", false},
		{[]string{"*"}, "anything", true},
		{[]string{}, "anything", false},
	}
	for _, c := range cases {
		if got := commandMatches(c.patterns, c.cmd); got != c.want {
			t.Errorf("commandMatches(%v, %q) = %v, want %v", c.patterns, c.cmd, got, c.want)
		}
	}
}
