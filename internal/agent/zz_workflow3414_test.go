package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

const spec3414 = `{
  "steps": [
    {"id":"tests","artifact_glob":"coverage.out"},
    {"id":"release","requires":["tests"],"mode":"block","on_commands":["git push*"]}
  ]
}`

func new3414Engine(t *testing.T, startedAt time.Time) *workflowEngine {
	t.Helper()
	e := newWFEngineWithSpec(t, spec3414)
	e.startedAt = startedAt
	return e
}

var push3414 = provider.ToolCallDelta{Name: "run_command", Arguments: json.RawMessage(`{"command":"git push origin main"}`)}

func TestIssue3414_CommandArtifactGroundsStep(t *testing.T) {
	e := new3414Engine(t, time.Now())
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.loadDir), "coverage.out"), []byte("mode: set"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.probeArtifactsOnDisk()
	if !e.isComplete("tests") {
		t.Fatal("#3414: fresh coverage.out on disk must ground the tests step")
	}
	if v := e.checkPreconditions(push3414.Name, push3414.Arguments); v != nil {
		t.Fatalf("release must unblock after command artifact grounding, got violation missing=%s", v.Missing)
	}
}

func TestIssue3414_StaleArtifactDoesNotGround(t *testing.T) {
	e := new3414Engine(t, time.Now())
	dir := filepath.Dir(e.loadDir)
	if err := os.WriteFile(filepath.Join(dir, "coverage.out"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "coverage.out"), old, old); err != nil {
		t.Fatal(err)
	}
	e.probeArtifactsOnDisk()
	if e.isComplete("tests") {
		t.Fatal("#3414: stale artifact (pre-anchor mtime) must not ground the step")
	}
	if v := e.checkPreconditions(push3414.Name, push3414.Arguments); v == nil {
		t.Fatal("release must stay blocked when the only artifact is stale")
	}
}

func TestIssue3414_PreconditionSelfHealOnDisk(t *testing.T) {
	e := new3414Engine(t, time.Now())
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.loadDir), "coverage.out"), []byte("mode: set"), 0o644); err != nil {
		t.Fatal(err)
	}

	if v := e.checkPreconditions(push3414.Name, push3414.Arguments); v != nil {
		t.Fatalf("precondition check must self-heal from fresh disk artifact, got violation missing=%s", v.Missing)
	}
	if !e.isComplete("tests") {
		t.Fatal("self-heal should persist the grounding")
	}
}

func TestIssue3414_ZeroAnchorNeverGrounds(t *testing.T) {
	e := newWFEngineWithSpec(t, spec3414)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.loadDir), "coverage.out"), []byte("mode: set"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.probeArtifactsOnDisk()
	if e.isComplete("tests") {
		t.Fatal("zero startedAt must never ground (freshness unprovable)")
	}
}

func TestIssue3414_NoArtifactStillBlocks(t *testing.T) {
	e := new3414Engine(t, time.Now())
	e.probeArtifactsOnDisk()
	if v := e.checkPreconditions(push3414.Name, push3414.Arguments); v == nil {
		t.Fatal("release must stay blocked with no artifact on disk")
	}
}

func TestIssue3425_SuperglobTopLevelGrounds(t *testing.T) {
	e := new3414Engine(t, time.Now())
	e.loadWorkflowSpec()
	e.steps["tests"] = WorkflowStep{ID: "tests", ArtifactGlob: "**/coverage.out", Mode: "block"}
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.loadDir), "coverage.out"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.probeArtifactsOnDisk()
	if !e.isComplete("tests") {
		t.Fatal("#3425: **/coverage.out must ground a TOP-LEVEL coverage.out (invariantGlobMatch semantics)")
	}
}

func TestIssue3425_SuperglobDeepGrounds(t *testing.T) {
	e := new3414Engine(t, time.Now())
	e.loadWorkflowSpec()
	e.steps["tests"] = WorkflowStep{ID: "tests", ArtifactGlob: "**/coverage.out", Mode: "block"}
	dir := filepath.Join(filepath.Dir(e.loadDir), "pkg", "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "coverage.out"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.probeArtifactsOnDisk()
	if !e.isComplete("tests") {
		t.Fatal("#3425: **/coverage.out must ground pkg/a/b/coverage.out (any depth)")
	}
}

func TestIssue3425_BothPathsAgreeOnBarePattern(t *testing.T) {
	e := new3414Engine(t, time.Now())
	e.loadWorkflowSpec()
	e.steps["tests"] = WorkflowStep{ID: "tests", ArtifactGlob: "coverage.out", Mode: "block"}

	e.recordCompletion("nested/dir/coverage.out")
	if !e.isComplete("tests") {
		t.Fatal("write path: bare pattern must ground nested artifact")
	}

	e2 := new3414Engine(t, time.Now())
	e2.loadWorkflowSpec()
	e2.steps["tests"] = WorkflowStep{ID: "tests", ArtifactGlob: "coverage.out", Mode: "block"}
	deep := filepath.Join(filepath.Dir(e2.loadDir), "nested", "dir")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "coverage.out"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2.probeArtifactsOnDisk()
	if !e2.isComplete("tests") {
		t.Fatal("#3425: probe path must agree with write path on bare pattern at depth")
	}
}
