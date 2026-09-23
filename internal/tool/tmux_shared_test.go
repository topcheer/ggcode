package tool

// Deterministic tmux tool coverage (sa-141): pure helpers, status rendering
// and pre-detection argument validation. No tmux server is required.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tmux"
)

func newTestTmuxManager(t *testing.T) *tmux.Manager {
	t.Helper()
	// Store file inside the test temp dir so Load() never touches the real
	// user pane store.
	return tmux.NewManagerWithStorePath(nil, "sa141-ws", filepath.Join(t.TempDir(), "panes.json"))
}

func TestToolTmuxLayoutNameSa141(t *testing.T) {
	if got := toolTmuxLayoutName(""); got != "default" {
		t.Fatalf("toolTmuxLayoutName(\"\") = %q", got)
	}
	if got := toolTmuxLayoutName("  "); got != "default" {
		t.Fatalf("toolTmuxLayoutName(blank) = %q", got)
	}
	if got := toolTmuxLayoutName(" dev "); got != "dev" {
		t.Fatalf("toolTmuxLayoutName(\" dev \") = %q, want dev", got)
	}
}

func TestTmuxStatusResultSa141(t *testing.T) {
	tool := NewTmuxTool(t.TempDir())
	mgr := newTestTmuxManager(t)

	// Detection error surfaces verbatim.
	r := tool.statusResult(mgr, nil, errors.New("boom"))
	if !r.IsError || !strings.Contains(r.Content, "tmux detect failed: boom") {
		t.Fatalf("detect error -> %+v", r)
	}
	// Environment nil: not detected.
	r = tool.statusResult(mgr, nil, nil)
	if r.IsError || r.Content != "tmux: not detected" {
		t.Fatalf("env nil -> %+v", r)
	}
	// Binary missing.
	r = tool.statusResult(mgr, &tmux.Environment{}, nil)
	if r.IsError || r.Content != "tmux: command not found" {
		t.Fatalf("unavailable -> %+v", r)
	}
	// Outside a session.
	r = tool.statusResult(mgr, &tmux.Environment{Available: true, Version: "3.4"}, nil)
	if r.IsError || !strings.Contains(r.Content, "not inside a tmux session") {
		t.Fatalf("not in tmux -> %+v", r)
	}
	// Fully available: renders workspace summary.
	r = tool.statusResult(mgr, &tmux.Environment{Available: true, InTmux: true, Version: "3.4"}, nil)
	if r.IsError || !strings.Contains(r.Content, "version: 3.4") || !strings.Contains(r.Content, "sa141-ws") {
		t.Fatalf("in tmux -> %+v", r)
	}
}

func TestTmuxExecuteValidationSa141(t *testing.T) {
	tool := NewTmuxTool(t.TempDir())
	ctx := context.Background()

	r, err := tool.Execute(ctx, json.RawMessage(`[1,2]`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}
	r, err = tool.Execute(ctx, json.RawMessage(`{"action":"  "}`))
	if err != nil || !r.IsError || r.Content != "action is required" {
		t.Fatalf("missing action -> (%+v,%v)", r, err)
	}
}

func TestTmuxToolMetadataSa141(t *testing.T) {
	tool := NewTmuxTool("/ws")
	if tool.Name() != "tmux" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	cloned, ok := tool.Clone().(*TmuxTool)
	if !ok || cloned == tool {
		t.Fatalf("Clone() = %T (same=%v)", cloned, cloned == tool)
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters() is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v", schema["type"])
	}
}
