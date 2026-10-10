package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #3835: commands-only steps (on_commands, no artifact_glob) were
// permanently incomplete - every completion path bails on the empty glob -
// so any step listing them in Requires deadlocked block mode on a spec that
// could never be satisfied. The successful guarded command itself must
// ground a commands-only step.

const spec3835 = `{
  "steps": [
    {"id":"tests","on_commands":["go test*"]},
    {"id":"release","requires":["tests"],"mode":"block","on_commands":["git push*"]},
    {"id":"hybrid","artifact_glob":"bin/app","on_commands":["make*"]}
  ]
}`

var push3835 = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"git push origin main"}`)}
var goTest3835 = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}

// Core deadlock: before the guarded command succeeds, release blocks on the
// commands-only tests step; after a successful `go test` the step grounds
// and the dependent unblocks (previously impossible - no artifact existed).
func TestIssue3835_CommandsOnlyStepGroundsOnSuccess(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	if v := e.checkPreconditions(push3835.Name, push3835.Arguments); v == nil || v.Missing != "tests" {
		t.Fatalf("release must initially block on commands-only tests step, got %v", v)
	}
	cmd, _ := parseRunCommandArgs(goTest3835.Arguments)
	e.recordCommandGrounding(cmd)
	if !e.isComplete("tests") {
		t.Fatal("#3835: successful guarded command must ground the commands-only step")
	}
	if v := e.checkPreconditions(push3835.Name, push3835.Arguments); v != nil {
		t.Fatalf("release must unblock after guarded command success, got violation missing=%s", v.Missing)
	}
}

// A FAILED guarded command must not ground (failure ledger keeps it
// incomplete and the block stays honest). recordCommandGrounding is only
// wired on success paths; assert the guard by checking a non-matching
// command does not complete the step.
func TestIssue3835_NonMatchingCommandDoesNotGround(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	e.recordCommandGrounding("echo hello")
	if e.isComplete("tests") {
		t.Fatal("a command matching no on_commands pattern must not ground the step")
	}
	if v := e.checkPreconditions(push3835.Name, push3835.Arguments); v == nil || v.Missing != "tests" {
		t.Fatalf("release must still block, got %v", v)
	}
}

// Hybrid steps (artifact_glob AND on_commands) keep artifact precedence:
// a successful matching command alone must NOT ground them - the glob is
// the stronger, filesystem-grounded proof.
func TestIssue3835_HybridStepKeepsArtifactPrecedence(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	e.recordCommandGrounding("make build")
	if e.isComplete("hybrid") {
		t.Fatal("hybrid step must only ground via its artifact_glob, not command success")
	}
}

// outstandingSteps must continue to ignore commands-only steps (no artifact
// to expect) - the fix must not surface them in the end-of-run audit.
func TestIssue3835_CommandsOnlyStepNotInAudit(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	e.recordCommandGrounding("go test ./...")
	for _, st := range e.outstandingSteps() {
		if st.ID == "tests" {
			t.Fatal("commands-only step must not appear in the end-of-run artifact audit")
		}
	}
}
