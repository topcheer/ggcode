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
	// #346: the fallback message must NOT reference host-specific slash
	// commands (Desktop/daemon/ACP have no /restart) — it should direct the
	// agent to ask the user for a manual restart instead.
	if strings.Contains(res.Content, "/restart") {
		t.Errorf("error must not reference the /restart slash command (unavailable on non-TUI hosts): %s", res.Content)
	}
	if !strings.Contains(res.Content, "manually") {
		t.Errorf("error should suggest asking the user to restart manually: %s", res.Content)
	}
	// #346: without a requester the tool must not be advertised to the LLM.
	if rt.Available() {
		t.Error("Available() must be false when no requester is injected")
	}
}

// TestRestartToolAvailableWithRequester verifies the tool is advertised once
// a host injects a requester (#346).
func TestRestartToolAvailableWithRequester(t *testing.T) {
	rt := &RestartTool{Requester: &fakeRestartRequester{}}
	if !rt.Available() {
		t.Error("Available() must be true when a requester is injected")
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
	if !strings.Contains(res.Content, "restart armed") {
		t.Errorf("result should describe the pending restart: %s", res.Content)
	}
	// #347: the result must tell the LLM the honest semantics — restart fires
	// at turn end (or fallback timeout), and no further tool calls should be
	// issued. The old text claimed "Fires when this turn ends" while a fixed
	// 1s timer exec'd mid-turn.
	if !strings.Contains(res.Content, "turn") {
		t.Errorf("result should reference turn-end semantics: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Do NOT issue any further tool calls") {
		t.Errorf("result should instruct the LLM to end its turn: %s", res.Content)
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
