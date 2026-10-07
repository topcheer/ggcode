package agent

// sa-85 tests: SkillTracer-style structural failure attribution - a
// violation must distinguish never-attempted / failed / no-artifact and
// name the local repair.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const specSa85 = `{
  "steps": [
    {"id":"tests","artifact_glob":"coverage.out","on_commands":["go test*"]},
    {"id":"release","requires":["tests"],"mode":"block","on_commands":["git push*"]}
  ]
}`

func pushArgs() json.RawMessage {
	return json.RawMessage(`{"command":"git push origin main"}`)
}

func testArgs() json.RawMessage {
	return json.RawMessage(`{"command":"go test ./..."}`)
}

// Failed prerequisite command -> violation carries the failing attempt and
// the attribution names WHERE it broke plus the local repair.
func TestWFTrace_FailedAttemptAttributed(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	e.recordAttempt("run_command", testArgs(), tool.Result{Content: "exit status 1: build failed in ./internal/agent", IsError: true})

	v := e.checkPreconditions("run_command", pushArgs())
	if v == nil {
		t.Fatal("push must stay blocked: tests step has no artifact")
	}
	if v.Missing != "tests" {
		t.Fatalf("counterexample must be tests, got %s", v.Missing)
	}
	if len(v.Attempts) != 1 || v.LastAttempt == nil {
		t.Fatalf("violation must carry the attempt, got %+v", v.Attempts)
	}
	if !v.LastAttempt.IsError || !strings.Contains(v.LastAttempt.ErrSnippet, "build failed") {
		t.Fatalf("attempt must record the error, got %+v", v.LastAttempt)
	}
	msg := wfAttemptAttribution(v)
	if !strings.Contains(msg, "FAILED") || !strings.Contains(msg, "go test ./...") || !strings.Contains(msg, "Local repair") {
		t.Fatalf("attribution must name the failed command and repair, got: %s", msg)
	}
}

// Successful command that produced no artifact -> the third SkillTracer
// outcome: ran fine, verifiable transition still missing.
func TestWFTrace_SuccessNoArtifactAttribution(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	e.recordAttempt("run_command", testArgs(), tool.Result{Content: "ok  all tests passed"})

	v := e.checkPreconditions("run_command", pushArgs())
	if v == nil || v.LastAttempt == nil {
		t.Fatal("violation with attempt expected")
	}
	if v.LastAttempt.IsError || v.LastAttempt.ProducedArtifact {
		t.Fatalf("expected success-without-artifact attempt, got %+v", v.LastAttempt)
	}
	msg := wfAttemptAttribution(v)
	if !strings.Contains(msg, "successfully") || !strings.Contains(msg, "no artifact") {
		t.Fatalf("attribution must say ran-fine-no-artifact, got: %s", msg)
	}
}

// Prerequisite command never ran -> attribution says so explicitly instead
// of leaving the agent to guess.
func TestWFTrace_NeverAttempted(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	v := e.checkPreconditions("run_command", pushArgs())
	if v == nil {
		t.Fatal("push must be blocked")
	}
	if len(v.Attempts) != 0 || v.LastAttempt != nil {
		t.Fatalf("no attempts expected, got %+v", v.Attempts)
	}
	msg := wfAttemptAttribution(v)
	if !strings.Contains(msg, "never executed") {
		t.Fatalf("attribution must say never executed, got: %s", msg)
	}
}

// A fresh artifact grounds the step; the trace shows ProducedArtifact and
// the gate lifts.
func TestWFTrace_GroundedAttemptLiftsGate(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	if err := os.WriteFile(filepath.Join(filepath.Dir(e.loadDir), "coverage.out"), []byte("mode: set"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.recordAttempt("run_command", testArgs(), tool.Result{Content: "ok"})

	if v := e.recentAttempts("tests", 3); len(v) != 1 || !v[0].ProducedArtifact {
		t.Fatalf("attempt must be grounded, got %+v", v)
	}
	if !e.isComplete("tests") {
		t.Fatal("grounding must complete the step")
	}
	if v := e.checkPreconditions("run_command", pushArgs()); v != nil {
		t.Fatalf("gate must lift after grounding, got %+v", v)
	}
}

// Unguarded and non-run_command calls leave no trace entries.
func TestWFTrace_NoTraceForUnguarded(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	e.recordAttempt("run_command", json.RawMessage(`{"command":"ls -la"}`), tool.Result{Content: "x"})
	e.recordAttempt("edit_file", json.RawMessage(`{"file_path":"a.go"}`), tool.Result{Content: "y"})

	if got := e.recentAttempts("tests", 10); len(got) != 0 {
		t.Fatalf("unguarded calls must not trace, got %+v", got)
	}
}

// The ledger is a sliding window: old attempts drop off.
func TestWFTrace_WindowCap(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	for i := 0; i < wfTraceWindow+10; i++ {
		e.recordAttempt("run_command", testArgs(), tool.Result{Content: "exit 1", IsError: true})
	}
	e.traceMu.Lock()
	n := len(e.trace)
	e.traceMu.Unlock()
	if n != wfTraceWindow {
		t.Fatalf("window must cap at %d, got %d", wfTraceWindow, n)
	}
}

// Violation attaches at most wfTraceMaxPerViolation attempts.
func TestWFTrace_ViolationAttemptCap(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	for i := 0; i < wfTraceMaxPerViolation+2; i++ {
		e.recordAttempt("run_command", testArgs(), tool.Result{Content: "exit 1", IsError: true})
	}
	v := e.checkPreconditions("run_command", pushArgs())
	if v == nil || len(v.Attempts) != wfTraceMaxPerViolation {
		t.Fatalf("violation must cap attempts at %d, got %d", wfTraceMaxPerViolation, len(v.Attempts))
	}
}

// Concurrent recording and attribution reads stay race-free (run -race).
func TestWFTrace_ConcurrentSafe(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = time.Now()
	e.loadWorkflowSpec()

	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 50; i++ {
				e.recordAttempt("run_command", testArgs(), tool.Result{Content: "exit 1", IsError: true})
				e.checkPreconditions("run_command", pushArgs())
			}
		}()
	}
	for w := 0; w < 4; w++ {
		<-done
	}
}

// Agent-side entry: nil-safe on a bare engine (workflowEngineLazy may return
// nil only when no working dir; recordAttempt itself must tolerate the
// nil engine the same way the rest of the engine does).
func TestWFTrace_RecordAttemptNilEngine(t *testing.T) {
	var e *workflowEngine
	e.recordAttempt("run_command", testArgs(), tool.Result{Content: "x"}) // must not panic
	if got := e.recentAttempts("tests", 3); got != nil {
		t.Fatalf("nil engine must return no attempts, got %+v", got)
	}
}

var _ = provider.ToolCallDelta{} // keep import stable for future agent-level tests
