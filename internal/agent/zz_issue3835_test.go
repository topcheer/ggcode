package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const spec3835 = `{
  "steps": [
    {"id":"tests","on_commands":["go test*"]},
    {"id":"release","requires":["tests"],"mode":"block","on_commands":["git push*"]},
    {"id":"hybrid","artifact_glob":"bin/app","on_commands":["make*"]}
  ]
}`

var push3835 = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"git push origin main"}`)}
var goTest3835 = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}
var makeBuild3835 = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"make build"}`)}

func TestIssue3835_CommandsOnlyStepGroundsOnSuccess(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	if v := e.checkPreconditions(push3835.Name, push3835.Arguments); v == nil || v.Missing != "tests" {
		t.Fatalf("release must initially block on commands-only tests step, got %v", v)
	}
	e.recordAttempt("run_command", goTest3835.Arguments, tool.Result{Content: "ok  all tests passed"})
	if !e.isComplete("tests") {
		t.Fatal("#3835: successful guarded command must ground the commands-only step")
	}
	if v := e.checkPreconditions(push3835.Name, push3835.Arguments); v != nil {
		t.Fatalf("release must unblock after guarded command success, got violation missing=%s", v.Missing)
	}
}

func TestIssue3835_FailedOrNonMatchingCommandDoesNotGround(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	e.recordAttempt("run_command", goTest3835.Arguments, tool.Result{Content: "exit status 1: FAIL", IsError: true})
	if e.isComplete("tests") {
		t.Fatal("a FAILED guarded command must not ground the step")
	}
	e.recordAttempt("run_command", json.RawMessage(`{"command":"echo hello"}`), tool.Result{Content: "ok"})
	if e.isComplete("tests") {
		t.Fatal("a command matching no on_commands pattern must not ground the step")
	}
	if v := e.checkPreconditions(push3835.Name, push3835.Arguments); v == nil || v.Missing != "tests" {
		t.Fatalf("release must still block, got %v", v)
	}
}

func TestIssue3835_HybridStepKeepsArtifactPrecedence(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	e.recordAttempt("run_command", makeBuild3835.Arguments, tool.Result{Content: "ok built"})
	if e.isComplete("hybrid") {
		t.Fatal("hybrid step must only ground via its artifact_glob, not command success")
	}
}

func TestIssue3835_CommandsOnlyStepNotInAudit(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3835)
	e.recordAttempt("run_command", goTest3835.Arguments, tool.Result{Content: "ok"})
	for _, st := range e.outstandingSteps() {
		if st.ID == "tests" {
			t.Fatal("commands-only step must not appear in the end-of-run artifact audit")
		}
	}
}
