package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

func newWhyFixture(t *testing.T) (*checkpoint.Manager, Why) {
	t.Helper()
	mgr := checkpoint.NewManager(50)
	mgr.StartRun("run-1")
	mgr.SetActiveIntent(checkpoint.EditIntent{
		TaskID:    "task-42",
		TaskDesc:  "Add rate limiting to the auth store",
		Rationale: "prevent credential-stuffing lockouts on the login endpoint",
	})
	mgr.SaveWithExistence("internal/auth/store.go", "old", "new", "edit_file", true)
	w := Why{Checkpoints: mgr, Workspace: "/w"}
	return mgr, w
}

func TestWhyCheckpointProvenanceHit(t *testing.T) {
	_, w := newWhyFixture(t)
	res, err := w.Execute(context.Background(), json.RawMessage(`{"target":"internal/auth/store.go:57"}`))
	if err != nil || res.IsError {
		t.Fatalf("execute failed: %v / %+v", err, res)
	}
	for _, want := range []string{"task-42", "Add rate limiting", "credential-stuffing"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("provenance output missing %q:\n%s", want, res.Content)
		}
	}
}

func TestWhySuffixPathMatch(t *testing.T) {
	_, w := newWhyFixture(t)
	res, err := w.Execute(context.Background(), json.RawMessage(`{"target":"auth/store.go"}`))
	if err != nil || res.IsError {
		t.Fatalf("execute failed: %v / %+v", err, res)
	}
	if !strings.Contains(res.Content, "task-42") {
		t.Fatalf("suffix path must still match checkpoint provenance:\n%s", res.Content)
	}
}

func TestWhyGracefulDegradationNoWiring(t *testing.T) {
	// No checkpoints and no registry: degrade to a clear, non-error answer.
	w := Why{}
	res, err := w.Execute(context.Background(), json.RawMessage(`{"target":"gone/file.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("degradation must not be an error: %+v", res)
	}
	if !strings.Contains(res.Content, "No checkpoint provenance") {
		t.Fatalf("degradation text missing:\n%s", res.Content)
	}
}

func TestWhyUnwiredCheckpointsFallsThrough(t *testing.T) {
	// Checkpoints wired but no provenance for the path -> falls through to
	// transcript search; with no session index in-process it must still be
	// a clean informational answer.
	mgr := checkpoint.NewManager(10)
	mgr.StartRun("run-2")
	mgr.SetActiveIntent(checkpoint.EditIntent{TaskID: "t", TaskDesc: "unrelated"})
	mgr.SaveWithExistence("other/file.go", "a", "b", "edit_file", true)
	w := Why{Checkpoints: mgr}
	res, err := w.Execute(context.Background(), json.RawMessage(`{"target":"auth/store.go"}`))
	if err != nil || res.IsError {
		t.Fatalf("execute failed: %v / %+v", err, res)
	}
	if strings.Contains(res.Content, "task-42") || strings.Contains(res.Content, "unrelated") {
		t.Fatalf("must not surface unrelated tasks:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "auth/store.go") {
		t.Fatalf("fallback must name the unresolved path:\n%s", res.Content)
	}
}
