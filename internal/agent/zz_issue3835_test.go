package agent

// #3835: commands-only steps (OnCommands, no artifact_glob) must ground on
// successful guarded-command execution - every completed.Store site required
// a non-empty glob, so a Requires edge to such a step deadlocked block mode
// forever, and the FAILED attribution quoted an "expected artifact" of "".

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func new3835Engine() *workflowEngine {
	return &workflowEngine{
		steps: map[string]WorkflowStep{
			"check": {ID: "check", Mode: "block", OnCommands: []string{"go vet*"}}, // no ArtifactGlob
			"build": {ID: "build", Mode: "block", OnCommands: []string{"go build*"}, Requires: []string{"check"}},
		},
		order:     []string{"check", "build"},
		loaded:    true,
		startedAt: time.Now(),
	}
}

func mk3835Args(t *testing.T, cmd string) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

// A: successful guarded command completes a commands-only step and unblocks
// dependents (the deadlock repro).
func TestIssue3835_CommandsOnlyStepGroundsOnSuccess(t *testing.T) {
	e := new3835Engine()

	// Before: go build is blocked because check is ungroundable.
	if v := e.checkPreconditions("run_command", mk3835Args(t, "go build ./...")); v == nil {
		t.Fatal("ungrounded prerequisite must block")
	}

	// go vet succeeds - the commands-only step must complete on that alone.
	e.recordAttempt("run_command", mk3835Args(t, "go vet ./..."), tool.Result{Content: "ok"})
	if !e.isComplete("check") {
		t.Fatal("commands-only step must ground on successful guarded command (#3835)")
	}

	// The dependent's guarded command is now allowed through.
	if v := e.checkPreconditions("run_command", mk3835Args(t, "go build ./...")); v != nil {
		t.Fatalf("grounded commands-only prerequisite must unblock dependents, got %+v", v)
	}
}

// B: a FAILED guarded command must NOT complete the step; the attribution
// must not quote an empty expected-artifact glob.
func TestIssue3835_FailedGuardedCommandKeepsStepOpen(t *testing.T) {
	e := new3835Engine()

	e.recordAttempt("run_command", mk3835Args(t, "go vet ./..."), tool.Result{IsError: true, Content: "vet: undeclared name"})
	if e.isComplete("check") {
		t.Fatal("failed guarded command must not complete a commands-only step")
	}

	v := e.checkPreconditions("run_command", mk3835Args(t, "go build ./..."))
	if v == nil {
		t.Fatal("still-blocked dependent must produce a violation")
	}
	msg := wfAttemptAttribution(v)
	if strings.Contains(msg, `artifact ""`) || strings.Contains(msg, `artifactifact ""`) {
		t.Fatalf("attribution must not quote an empty glob, got: %s", msg)
	}
	if !strings.Contains(msg, "successful run completes the step") {
		t.Fatalf("commands-only FAILED attribution must point at re-running the command, got: %s", msg)
	}
}

// C: steps WITH a glob keep artifact precedence - command success alone must
// not ground them when no fresh artifact exists.
func TestIssue3835_GlobStepsKeepArtifactPrecedence(t *testing.T) {
	e := &workflowEngine{
		steps: map[string]WorkflowStep{
			"gen": {ID: "gen", Mode: "block", OnCommands: []string{"go generate*"}, ArtifactGlob: "zz_nonexistent_gen_output.txt"},
			"bd":  {ID: "bd", Mode: "block", OnCommands: []string{"go build*"}, Requires: []string{"gen"}},
		},
		order:     []string{"gen", "bd"},
		loaded:    true,
		startedAt: time.Now(),
	}
	e.recordAttempt("run_command", mk3835Args(t, "go generate ./..."), tool.Result{Content: "ok"})
	if e.isComplete("gen") {
		t.Fatal("glob step must not ground on command success without the artifact")
	}
	if v := e.checkPreconditions("run_command", mk3835Args(t, "go build ./...")); v == nil {
		t.Fatal("dependent must stay blocked until the artifact appears")
	}
}
