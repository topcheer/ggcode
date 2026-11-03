package agent

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

func wfTestAnchor() time.Time { return time.Now().Add(-time.Second) }

func TestWFTrace_FailedAttemptAttributed(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
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

func TestWFTrace_SuccessNoArtifactAttribution(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
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

func TestWFTrace_NeverAttempted(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
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

func TestWFTrace_GroundedAttemptLiftsGate(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
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

func TestWFTrace_NoTraceForUnguarded(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
	e.loadWorkflowSpec()

	e.recordAttempt("run_command", json.RawMessage(`{"command":"ls -la"}`), tool.Result{Content: "x"})
	e.recordAttempt("edit_file", json.RawMessage(`{"file_path":"a.go"}`), tool.Result{Content: "y"})

	if got := e.recentAttempts("tests", 10); len(got) != 0 {
		t.Fatalf("unguarded calls must not trace, got %+v", got)
	}
}

func TestWFTrace_WindowCap(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
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

func TestWFTrace_ViolationAttemptCap(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
	e.loadWorkflowSpec()

	for i := 0; i < wfTraceMaxPerViolation+2; i++ {
		e.recordAttempt("run_command", testArgs(), tool.Result{Content: "exit 1", IsError: true})
	}
	v := e.checkPreconditions("run_command", pushArgs())
	if v == nil || len(v.Attempts) != wfTraceMaxPerViolation {
		t.Fatalf("violation must cap attempts at %d, got %d", wfTraceMaxPerViolation, len(v.Attempts))
	}
}

func TestWFTrace_ConcurrentSafe(t *testing.T) {
	e := newWFEngineWithSpec(t, specSa85)
	e.startedAt = wfTestAnchor()
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

func TestWFTrace_RecordAttemptNilEngine(t *testing.T) {
	var e *workflowEngine
	e.recordAttempt("run_command", testArgs(), tool.Result{Content: "x"})
	if got := e.recentAttempts("tests", 3); got != nil {
		t.Fatalf("nil engine must return no attempts, got %+v", got)
	}
}

var _ = provider.ToolCallDelta{}
