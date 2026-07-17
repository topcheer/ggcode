package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTmuxToolSchemaExposesLifecycleActions(t *testing.T) {
	tool := NewTmuxTool("/tmp/workspace")
	if got, want := tool.Name(), "tmux"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	for _, want := range []string{"pane creation", "output capture", "close"} {
		if !strings.Contains(tool.Description(), want) {
			t.Fatalf("Description() should mention %q, got %q", want, tool.Description())
		}
	}
	params := string(tool.Parameters())
	for _, want := range []string{"status", "split", "popup", "list", "logs", "layouts", "layout", "setup", "save_layout", "delete_layout", "rename_layout", "refresh", "restore", "rerun", "prune", "capture", "focus", "stop", "close", "pane_id"} {
		if !strings.Contains(params, want) {
			t.Fatalf("Parameters() should mention %q, got %s", want, params)
		}
	}
}

func TestTmuxToolRequiresAction(t *testing.T) {
	tool := NewTmuxTool(t.TempDir())
	input, _ := json.Marshal(map[string]string{"description": "Check tmux"})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute returned system error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "action is required") {
		t.Fatalf("expected action error, got %+v", res)
	}
}

func TestTmuxToolCloneSharesManager(t *testing.T) {
	workspace := t.TempDir()
	tool := NewTmuxTool(workspace)

	clone, ok := tool.Clone().(*TmuxTool)
	if !ok {
		t.Fatalf("Clone returned %T", tool.Clone())
	}
	if clone.WorkingDir != tool.WorkingDir {
		t.Fatalf("clone WorkingDir = %q, want %q", clone.WorkingDir, tool.WorkingDir)
	}
	if clone.Manager != tool.Manager {
		t.Fatal("clone should share tmux manager so tool state is process-wide")
	}
}

func TestRegisterBuiltinToolsSkipsTmuxOutsideTmuxSession(t *testing.T) {
	t.Setenv("TMUX", "")
	registry := NewRegistry()
	if err := RegisterBuiltinTools(registry, nil, t.TempDir()); err != nil {
		t.Fatalf("RegisterBuiltinTools error: %v", err)
	}
	if _, ok := registry.Get("tmux"); ok {
		t.Fatal("tmux tool should not be registered outside a tmux session")
	}
}
