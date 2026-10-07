package agent

// Issue #3512: multi_file_write creating a NEW file must record
// Existed=false on the checkpoint so undo REMOVES the file instead of
// writing back "" and leaving a 0-byte husk (issue #554 B, multi-file leg).
// The single-file path (executeFileTool) already used SaveWithExistence;
// executeMultiFileTool called the unconditional Save.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newIssue3512Agent(t *testing.T) (*Agent, *checkpoint.Manager) {
	t.Helper()
	cpMgr := checkpoint.NewManager(50)
	a := &Agent{
		contextManager: ctxpkg.NewManager(200000),
		tools:          tool.NewRegistry(),
	}
	a.SetCheckpointManager(cpMgr)
	return a, cpMgr
}

// Tool-level: PreviewChanges must classify create vs overwrite.
func TestIssue3512_PreviewChangesFlagsExistence(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	mfw := tool.MultiFileWrite{}
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": existing, "content": "new"},
			{"path": filepath.Join(dir, "fresh.txt"), "content": "created"},
		},
	})
	plans, err := mfw.PreviewChanges(json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	for _, p := range plans {
		if filepath.Base(p.Path) == "exists.txt" && !p.Existed {
			t.Error("pre-existing file must plan Existed=true")
		}
		if filepath.Base(p.Path) == "fresh.txt" && p.Existed {
			t.Error("missing file must plan Existed=false (create)")
		}
	}
}

// Agent-level end-to-end: undo after a creating multi_file_write must REMOVE
// the file, not truncate it to 0 bytes.
func TestIssue3512_UndoRemovesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, "created.txt")
	a, cpMgr := newIssue3512Agent(t)

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{{"path": created, "content": "hello world"}},
	})
	mfw := tool.MultiFileWrite{}
	res := a.executeMultiFileTool(context.Background(), mfw, mfw,
		provider.ToolCallDelta{Name: "multi_file_write", Arguments: json.RawMessage(input)},
		hooks.HookEnv{})
	if res.IsError {
		t.Fatalf("multi_file_write failed: %s", res.Content)
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("file should exist after write: %v", err)
	}

	cp, err := cpMgr.Undo("agent")
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}
	if cp.FilePath != created {
		t.Fatalf("undo reverted wrong file: %s", cp.FilePath)
	}
	if cp.Existed {
		t.Fatal("checkpoint for a created file must record Existed=false (#554 B multi-file leg)")
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		data, _ := os.ReadFile(created)
		t.Fatalf("undo must REMOVE the created file; stat err=%v, content=%q", err, string(data))
	}
}

// Agent-level: overwriting a PRE-EXISTING file must keep Existed=true so
// undo restores the old content (regression guard for the same change).
func TestIssue3512_UndoRestoresOverwrittenFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, cpMgr := newIssue3512Agent(t)

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{{"path": target, "content": "replaced"}},
	})
	mfw := tool.MultiFileWrite{}
	res := a.executeMultiFileTool(context.Background(), mfw, mfw,
		provider.ToolCallDelta{Name: "multi_file_write", Arguments: json.RawMessage(input)},
		hooks.HookEnv{})
	if res.IsError {
		t.Fatalf("multi_file_write failed: %s", res.Content)
	}

	cp, err := cpMgr.Undo("agent")
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}
	if !cp.Existed {
		t.Fatal("checkpoint for a pre-existing file must record Existed=true")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("undo must restore original content, got %q", string(data))
	}
}
