package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type g68FakeCaller struct {
	tasks     bool
	taskCalls int
	syncCalls int
	syncRes   *CallToolResult
	taskErr   error
}

func (f *g68FakeCaller) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	f.syncCalls++
	if f.syncRes != nil {
		return f.syncRes, nil
	}
	return &CallToolResult{Content: []ToolContent{{Type: "text", Text: "sync-ok"}}}, nil
}

func (f *g68FakeCaller) CallToolAsTask(ctx context.Context, name string, args map[string]interface{}, opts TaskRequestOptions) (*CallToolResult, *Task, error) {
	f.taskCalls++
	if f.taskErr != nil {
		return nil, nil, f.taskErr
	}
	return &CallToolResult{Content: []ToolContent{{Type: "text", Text: "task-ok"}}}, nil, nil
}

func (f *g68FakeCaller) HasTasks() bool { return f.tasks }

func TestG68TasksAdapterUpgradesTasksCapableCaller(t *testing.T) {
	f := &g68FakeCaller{tasks: true}
	mt := &mcpTool{name: "mcp__srv__t", caller: f, toolName: "t", srvName: "srv"}
	res, err := mt.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.taskCalls != 1 || f.syncCalls != 0 {
		t.Fatalf("want 1 task call / 0 sync calls, got task=%d sync=%d", f.taskCalls, f.syncCalls)
	}
	if res.IsError || !strings.Contains(res.Content, "task-ok") {
		t.Fatalf("want task-ok result content, got IsError=%v content=%q", res.IsError, res.Content)
	}
}

func TestG68TasksAdapterFallsBackWithoutTasks(t *testing.T) {
	f := &g68FakeCaller{tasks: false}
	mt := &mcpTool{name: "mcp__srv__s", caller: f, toolName: "s", srvName: "srv"}
	res, err := mt.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.syncCalls != 1 || f.taskCalls != 0 {
		t.Fatalf("want 1 sync call / 0 task calls, got sync=%d task=%d", f.syncCalls, f.taskCalls)
	}
	if res.IsError || !strings.Contains(res.Content, "sync-ok") {
		t.Fatalf("want sync-ok result content, got IsError=%v content=%q", res.IsError, res.Content)
	}
}

func TestG68TasksTerminalFailureSurfaces(t *testing.T) {
	f := &g68FakeCaller{tasks: true, taskErr: errors.New("task input_required: missing parameter 'q'")}
	mt := &mcpTool{name: "mcp__srv__e", caller: f, toolName: "e", srvName: "srv"}
	res, err := mt.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute returned go-level error (want in-band IsError): %v", err)
	}
	if !res.IsError {
		t.Fatalf("want IsError=true for terminal task failure, got content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "input_required") {
		t.Fatalf("failure reason not surfaced: %q", res.Content)
	}
}
