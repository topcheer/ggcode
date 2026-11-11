package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

type fakeRestartRequester struct {
	called    bool
	debugMode bool
}

func (f *fakeRestartRequester) RequestRestart(reason string, debugMode bool) {
	f.called = true
	f.debugMode = debugMode
}

func TestRestartToolNoRequester(t *testing.T) {
	rt := &RestartTool{}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError result when no requester injected, got: %s", res.Content)
	}

	if strings.Contains(res.Content, "/restart") {
		t.Errorf("error must not reference the /restart slash command (unavailable on non-TUI hosts): %s", res.Content)
	}
	if !strings.Contains(res.Content, "manually") {
		t.Errorf("error should suggest asking the user to restart manually: %s", res.Content)
	}

	if rt.Available() {
		t.Error("Available() must be false when no requester is injected")
	}
}

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

// fakeResumeNudger captures the nudge arming call so tests can pin the
// stable-ID contract on the tool side (#5425).
type fakeResumeNudger struct {
	id    string
	fire  time.Time
	prom  string
	queue bool
}

func (f *fakeResumeNudger) CreateOneShotWithID(id string, fireAt time.Time, prompt string, queueIfBusy bool) (cron.Job, error) {
	f.id, f.fire, f.prom, f.queue = id, fireAt, prompt, queueIfBusy
	return cron.Job{ID: id, NextFire: fireAt, Persist: true}, nil
}

// TestRestartToolArmsStableNudgeID pins that the restart tool arms the resume
// nudge under the stable RestartResumeNudgeID (replace semantics upstream),
// so a double restart can never stack duplicate nudges.
func TestRestartToolArmsStableNudgeID(t *testing.T) {
	req := &fakeRestartRequester{}
	nudger := &fakeResumeNudger{}
	rt := &RestartTool{Requester: req, Nudger: nudger}
	res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"binary updated"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if nudger.id != cron.RestartResumeNudgeID {
		t.Errorf("nudge must arm under stable ID %q, got %q", cron.RestartResumeNudgeID, nudger.id)
	}
	if !nudger.queue {
		t.Error("nudge must be queue_if_busy=true")
	}
	if nudger.prom != restartResumeNudgePrompt {
		t.Errorf("nudge prompt = %q, want %q", nudger.prom, restartResumeNudgePrompt)
	}
	if d := nudger.fire.Sub(time.Now()); d < 2*time.Minute || d > 4*time.Minute {
		t.Errorf("nudge fire time should be ~3min out, got %s", d)
	}
}
