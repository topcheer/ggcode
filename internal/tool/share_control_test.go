package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeShareController struct {
	active bool
	// startErr simulates a frontend failure.
	startErr error
	stopped  bool
	started  bool
}

func (f *fakeShareController) StartAgentShare() (ShareStartResult, error) {
	if f.startErr != nil {
		return ShareStartResult{}, f.startErr
	}
	f.started = true
	f.active = true
	return ShareStartResult{ConnectURL: "https://relay.example/join?room=AB12&token=tok", RoomID: "AB12"}, nil
}

func (f *fakeShareController) StopAgentShare() error {
	f.stopped = true
	f.active = false
	return nil
}

func (f *fakeShareController) ShareActive() bool { return f.active }

func runTool(t *testing.T, tr interface {
	Execute(context.Context, json.RawMessage) (Result, error)
}) Result {
	t.Helper()
	res, err := tr.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

func TestStartShareToolNoController(t *testing.T) {
	res := runTool(t, StartShareTool{})
	if !res.IsError {
		t.Fatal("expected error result when controller is nil")
	}
	if !strings.Contains(res.Content, "not available") {
		t.Fatalf("unexpected content: %s", res.Content)
	}
}

func TestStartShareToolAlreadyActive(t *testing.T) {
	fc := &fakeShareController{active: true}
	res := runTool(t, StartShareTool{Controller: fc})
	if !res.IsError {
		t.Fatal("expected error when share already active")
	}
	if !strings.Contains(res.Content, "already active") {
		t.Fatalf("unexpected content: %s", res.Content)
	}
}

func TestStartShareToolSuccessRelaysFullURL(t *testing.T) {
	fc := &fakeShareController{}
	res := runTool(t, StartShareTool{Controller: fc})
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !fc.started {
		t.Fatal("controller not started")
	}
	// The full connect URL must be relayed verbatim for the user.
	if !strings.Contains(res.Content, "https://relay.example/join?room=AB12&token=tok") {
		t.Fatalf("full connect URL missing from result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "VERBATIM") {
		t.Fatalf("result must instruct the agent to show the URL verbatim: %s", res.Content)
	}
}

func TestStartShareToolStartFailure(t *testing.T) {
	fc := &fakeShareController{startErr: errors.New("relay unreachable")}
	res := runTool(t, StartShareTool{Controller: fc})
	if !res.IsError {
		t.Fatal("expected error result on start failure")
	}
	if !strings.Contains(res.Content, "relay unreachable") {
		t.Fatalf("failure reason missing: %s", res.Content)
	}
}

func TestStopShareToolNoController(t *testing.T) {
	res := runTool(t, StopShareTool{})
	if !res.IsError {
		t.Fatal("expected error result when controller is nil")
	}
}

func TestStopShareToolInactiveIsNoop(t *testing.T) {
	fc := &fakeShareController{}
	res := runTool(t, StopShareTool{Controller: fc})
	if res.IsError {
		t.Fatalf("inactive stop should be a no-op success: %s", res.Content)
	}
	if fc.stopped {
		t.Fatal("inactive stop must not call through")
	}
}

func TestStopShareToolSuccess(t *testing.T) {
	fc := &fakeShareController{active: true}
	res := runTool(t, StopShareTool{Controller: fc})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !fc.stopped || fc.active {
		t.Fatal("controller not stopped")
	}
}

func TestShareToolDescriptionsReflectState(t *testing.T) {
	// nil controller: degraded availability wording.
	if d := (StartShareTool{}).Description(); !strings.Contains(d, "Not available") {
		t.Fatalf("nil controller description should degrade: %s", d)
	}
	// active: start warns against double start.
	fc := &fakeShareController{active: true}
	if d := (StartShareTool{Controller: fc}).Description(); !strings.Contains(d, "ALREADY active") {
		t.Fatalf("active description should warn: %s", d)
	}
	// active: stop offers itself.
	if d := (StopShareTool{Controller: fc}).Description(); !strings.Contains(d, "Stop the active") {
		t.Fatalf("stop description mismatch: %s", d)
	}
}
