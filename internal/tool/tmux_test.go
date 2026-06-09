package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tmux"
)

func TestTmuxToolSchemaExposesLifecycleActions(t *testing.T) {
	tool := NewTmuxTool("/tmp/workspace")
	if got, want := tool.Name(), "tmux"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	for _, want := range []string{"create panes", "capture pane output", "refresh managed pane state", "close panes"} {
		if !strings.Contains(tool.Description(), want) {
			t.Fatalf("Description() should mention %q, got %q", want, tool.Description())
		}
	}
	params := string(tool.Parameters())
	for _, want := range []string{"status", "split", "popup", "list", "refresh", "capture", "focus", "close", "pane_id"} {
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

func TestTmuxToolCloneCopiesManagedPaneState(t *testing.T) {
	tool := NewTmuxTool("/tmp/workspace")
	tool.panes["%1"] = tmuxPaneForTest("%1", true)

	clone, ok := tool.Clone().(*TmuxTool)
	if !ok {
		t.Fatalf("Clone returned %T", tool.Clone())
	}
	if clone.WorkingDir != tool.WorkingDir {
		t.Fatalf("clone WorkingDir = %q, want %q", clone.WorkingDir, tool.WorkingDir)
	}
	clone.panes["%1"] = tmuxPaneForTest("%1", false)
	if !tool.panes["%1"].Alive {
		t.Fatal("clone mutation should not mutate original pane state")
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

func tmuxPaneForTest(id string, alive bool) tmux.Pane {
	return tmux.Pane{ID: id, Purpose: "test", Command: "go test ./...", Workspace: "/tmp/workspace", Alive: alive}
}
