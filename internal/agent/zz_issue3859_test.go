//go:build goolm

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #3859: two grounding gaps.
//  A: start_command success never probed the disk for step artifacts
//     (probeArtifactsOnDisk was run_command-only).
//  B: warn-mode branches (workflow warn + invariant warn) returned early
//     and skipped grounding entirely - a command that both hit a warn
//     guard and produced a step's artifact left that step un-grounded
//     (commands-only soft deadlock).

const spec3859 = `{
  "steps": [
    {"id":"build","artifact_glob":"bin/*"},
    {"id":"release","artifact_glob":"","requires":["build"],"mode":"block","on_commands":["git push*"]}
  ]
}`

func agent3859(t *testing.T) *Agent {
	t.Helper()
	dir := t.TempDir()
	gg := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(gg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gg, workflowSpecFileName), []byte(spec3859), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "app"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAgent(nil, nil, "sys", 5)
	a.workingDir = dir
	return a
}

// A: a start_command success must ground the artifact-producing step.
func TestIssue3859_StartCommandGroundsArtifacts(t *testing.T) {
	a := agent3859(t)
	e := a.workflowEngineLazy()
	if e == nil {
		t.Fatal("engine not loaded")
	}
	if e.isComplete("build") {
		t.Fatal("precondition: build must start incomplete")
	}
	tc := provider.ToolCallDelta{Name: "start_command", Arguments: json.RawMessage(`{"command":"make build"}`)}
	a.groundWorkflowAfterTool(tc, tool.Result{Content: "started"})
	if !e.isComplete("build") {
		t.Fatal("start_command success must probe artifacts and ground 'build' (#3859 A)")
	}
}

// B: the grounding helper is invoked on the warn path - simulate by
// calling it with a warn-marked result shape (res not IsError) directly;
// the wiring (call before return in both warn branches) is pinned by the
// same helper, and an IsError result must NOT ground.
func TestIssue3859_GroundingHelperGatesOnError(t *testing.T) {
	a := agent3859(t)
	e := a.workflowEngineLazy()
	tc := provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"make build"}`)}

	a.groundWorkflowAfterTool(tc, tool.Result{Content: "boom", IsError: true})
	if e.isComplete("build") {
		t.Fatal("failed commands must not ground steps")
	}

	a.groundWorkflowAfterTool(tc, tool.Result{Content: "ok"})
	if !e.isComplete("build") {
		t.Fatal("successful run_command must ground 'build' via the shared helper")
	}
}
