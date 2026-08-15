package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeRestartRequester struct {
	called    bool
	debugMode bool
}

func (f *fakeRestartRequester) RequestRestart(debugMode bool) {
	f.called = true
	f.debugMode = debugMode
}

func TestRestartToolNoRequester(t *testing.T) {
	rt := &RestartTool{} // no requester injected (headless)
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError result when no requester injected, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "/restart") {
		t.Errorf("error should point the user to the /restart slash command: %s", res.Content)
	}
}

func TestRestartToolRequestsRestart(t *testing.T) {
	req := &fakeRestartRequester{}
	rt := &RestartTool{Requester: req}

	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"binary updated","debug":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !req.called {
		t.Error("RequestRestart was not called")
	}
	if !req.debugMode {
		t.Error("debug flag was not propagated")
	}
	if !strings.Contains(res.Content, "restart requested") {
		t.Errorf("result should describe the pending restart: %s", res.Content)
	}
}

func TestRestartToolInvalidInput(t *testing.T) {
	rt := &RestartTool{Requester: &fakeRestartRequester{}}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{not json`))
	if err != nil {
		t.Fatalf("tool must return IsError result, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for invalid JSON")
	}
}

func TestRestartToolSchema(t *testing.T) {
	rt := &RestartTool{}
	var schema map[string]any
	if err := json.Unmarshal(rt.Parameters(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("schema missing properties")
	}
	if _, ok := props["reason"]; !ok {
		t.Error("schema missing required reason param")
	}
	req, _ := schema["required"].([]any)
	found := false
	for _, r := range req {
		if r == "reason" {
			found = true
		}
	}
	if !found {
		t.Error("reason not in required list")
	}
}
