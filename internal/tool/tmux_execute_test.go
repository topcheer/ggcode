package tool

// tmux Execute dispatch coverage (sa-141). Only read-only detection paths
// are exercised (tmux -V); panes are never created or killed.

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestTmuxExecuteStatusSa141(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX", "") // force outside-session detection
	tool := NewTmuxTool(t.TempDir())
	ctx := context.Background()

	r, err := tool.Execute(ctx, json.RawMessage(`{bad`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}
	r, err = tool.Execute(ctx, json.RawMessage(`{"action":""}`))
	if err != nil || !r.IsError || r.Content != "action is required" {
		t.Fatalf("missing action -> (%+v,%v)", r, err)
	}

	r, err = tool.Execute(ctx, json.RawMessage(`{"action":"STATUS"}`))
	if err != nil || r.IsError {
		t.Fatalf("status -> (%+v,%v)", r, err)
	}
	if !strings.Contains(r.Content, "not inside a tmux session") {
		t.Fatalf("status content = %q, want outside-session report", r.Content)
	}

	// Non-status action outside a session is rejected before dispatch.
	r, err = tool.Execute(ctx, json.RawMessage(`{"action":"split","command":"true"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "tmux is not available") {
		t.Fatalf("split outside session -> (%+v,%v)", r, err)
	}
}
