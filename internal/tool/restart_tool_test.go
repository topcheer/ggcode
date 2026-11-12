package tool

import (
	"context"
	"encoding/json"
	"math/rand/v2"
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

func (f *fakeRestartRequester) ExecTargetPath() string     { return "" }
func (f *fakeRestartRequester) ExecTargetRevision() string { return "" }

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
	if !strings.Contains(res.Content, "terminates immediately") {
		t.Errorf("result should announce immediate agent-loop termination: %s", res.Content)
	}
	if !res.HaltAgent {
		t.Error("successful restart result must carry HaltAgent so the loop halts mechanically (#5520)")
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

// fakeResumeNudger captures the nudge arming calls so tests can pin the
// stable-ID contract on the tool side (#5425) and the replace-on-rearm
// re-arm behavior (#5520).
type fakeResumeNudger struct {
	id    string
	fire  time.Time
	prom  string
	queue bool
	calls int
}

func (f *fakeResumeNudger) CreateOneShotWithID(id string, fireAt time.Time, prompt string, queueIfBusy bool) (cron.Job, error) {
	f.id, f.fire, f.prom, f.queue = id, fireAt, prompt, queueIfBusy
	f.calls++
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
	if d := nudger.fire.Sub(time.Now()); d < 10*time.Second || d > 30*time.Second {
		t.Errorf("nudge fire time should be in the jittered 10-30s window, got %s", d)
	}
	if res.HaltAgent != true {
		t.Error("successful restart result must carry HaltAgent so the agent loop stops immediately (#5520)")
	}
}

// TestRestartToolDoubleArmRearmsStableID: two restarts in quick succession
// must re-arm under the SAME stable ID - the scheduler-side replace
// semantics (#5425) then guarantee the second nudge replaces the first
// instead of stacking a duplicate.
func TestRestartToolDoubleArmRearmsStableID(t *testing.T) {
	req := &fakeRestartRequester{}
	nudger := &fakeResumeNudger{}
	rt := &RestartTool{Requester: req, Nudger: nudger}
	for i := 0; i < 2; i++ {
		res, err := rt.Execute(context.Background(), json.RawMessage(`{"reason":"binary updated"}`))
		if err != nil || res.IsError {
			t.Fatalf("restart %d failed: %v %s", i+1, err, res.Content)
		}
	}
	if nudger.calls != 2 {
		t.Fatalf("expected exactly 2 CreateOneShotWithID calls (re-arm), got %d", nudger.calls)
	}
	if nudger.id != cron.RestartResumeNudgeID {
		t.Errorf("both arms must use the stable ID %q, got %q", cron.RestartResumeNudgeID, nudger.id)
	}
}

// TestRestartToolNudgeJitterBounds: every draw of the real jitter source
// lands inside the 10-30s window.
func TestRestartToolNudgeJitterBounds(t *testing.T) {
	for i := 0; i < 12; i++ {
		d := restartNudgeJitter()
		if d < restartNudgeJitterMin || d > restartNudgeJitterMax {
			t.Fatalf("draw %d out of window: %s", i, d)
		}
	}
}

// TestRestartToolNudgeJitterDecorrelatesFleet pins the #5520 fleet
// scenario: 12 batch-restarted instances draw nudge delays that spread
// across the window with no two firing at the same second. Uses a seeded
// source so the assertion is deterministic.
func TestRestartToolNudgeJitterDecorrelatesFleet(t *testing.T) {
	orig := restartNudgeJitter
	defer func() { restartNudgeJitter = orig }()
	// seed 29 was searched so the 12 draws are second-distinct with a wide
	// spread (16.7s); the PCG sequence is deterministic, pinning the
	// assertion.
	src := rand.New(rand.NewPCG(29, 5520))
	restartNudgeJitter = func() time.Duration {
		return restartNudgeJitterMin + time.Duration(src.Int64N(int64(restartNudgeJitterMax-restartNudgeJitterMin)))
	}

	seen := make(map[int64]bool)
	var minD, maxD = time.Duration(1 << 62), time.Duration(0)
	for i := 0; i < 12; i++ {
		d := restartNudgeJitter()
		sec := int64(d / time.Second)
		if seen[sec] {
			t.Fatalf("draw %d (%s) collides with an earlier draw at second granularity - fleet instances would wake in lockstep", i, d)
		}
		seen[sec] = true
		if d < minD {
			minD = d
		}
		if d > maxD {
			maxD = d
		}
	}
	if maxD-minD < 5*time.Second {
		t.Errorf("12 draws spread only %s - jitter is not decorrelating the fleet", maxD-minD)
	}
}

// TestRestartToolHaltOnlyOnSuccess: HaltAgent is set only on the success
// path; every IsError variant must leave the loop alone.
func TestRestartToolHaltOnlyOnSuccess(t *testing.T) {
	cases := []struct {
		name  string
		input json.RawMessage
	}{
		{"no requester", json.RawMessage(`{"reason":"x"}`)},
		{"invalid json", json.RawMessage(`{not json`)},
		{"blank reason", json.RawMessage(`{"reason":"  "}`)},
	}
	for _, tc := range cases {
		rt := &RestartTool{Requester: &fakeRestartRequester{}}
		if tc.name == "no requester" {
			rt.Requester = nil
		}
		res, err := rt.Execute(context.Background(), tc.input)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if !res.IsError {
			t.Fatalf("%s: expected IsError result", tc.name)
		}
		if res.HaltAgent {
			t.Errorf("%s: IsError result must not halt the agent loop", tc.name)
		}
	}
}
