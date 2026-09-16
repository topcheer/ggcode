package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/toolreplay"
)

// tapeStubTool is a minimal tool.Tool whose Execute counts invocations so
// tests can prove the real tool was (or was not) called.
type tapeStubTool struct {
	name  string
	calls int
	resp  tool.Result
	err   error
}

func (s *tapeStubTool) Name() string                { return s.name }
func (s *tapeStubTool) Description() string         { return "stub tool for tape tests" }
func (s *tapeStubTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *tapeStubTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	s.calls++
	return s.resp, s.err
}

func TestParseToolTapeEnv(t *testing.T) {
	cases := []struct {
		raw     string
		mode    toolTapeMode
		path    string
		wantErr bool
	}{
		{raw: "", mode: toolTapeOff},
		{raw: "   ", mode: toolTapeOff},
		{raw: "record:/tmp/a.tape.json", mode: toolTapeRecord, path: "/tmp/a.tape.json"},
		{raw: "RECORD:/tmp/a.tape.json", mode: toolTapeRecord, path: "/tmp/a.tape.json"},
		{raw: "replay:/tmp/b.tape.json", mode: toolTapeReplay, path: "/tmp/b.tape.json"},
		{raw: "Replay: ./rel.tape.json", mode: toolTapeReplay, path: "./rel.tape.json"},
		{raw: "/tmp/just-a-path.json", mode: toolTapeOff, wantErr: true},
		{raw: "record:", mode: toolTapeOff, wantErr: true},
		{raw: "banana:/tmp/x.json", mode: toolTapeOff, wantErr: true},
	}
	for _, c := range cases {
		mode, path, err := parseToolTapeEnv(c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("parseToolTapeEnv(%q) err=%v, wantErr=%v", c.raw, err, c.wantErr)
			continue
		}
		if err == nil && (mode != c.mode || path != c.path) {
			t.Errorf("parseToolTapeEnv(%q) = (%d, %q), want (%d, %q)", c.raw, mode, path, c.mode, c.path)
		}
	}
}

func TestToolTapeRecordReplayRoundtrip(t *testing.T) {
	dir := t.TempDir()
	tapePath := filepath.Join(dir, "session.tape.json")
	ctx := context.Background()

	// --- Record phase: two calls with distinct inputs, one with images. ---
	stub := &tapeStubTool{name: "read_file", resp: tool.Result{Content: "hello world"}}
	a := &Agent{toolTape: &toolTapeState{
		tape: toolreplay.NewTape(),
		mode: toolTapeRecord,
		path: tapePath,
	}}
	argsA := json.RawMessage(`{"path":"a.txt"}`)
	argsB := json.RawMessage(`{"path":"b.txt"}`)
	if _, err := a.safeExecute(stub, ctx, argsA); err != nil {
		t.Fatalf("record call A: %v", err)
	}
	if _, err := a.safeExecute(stub, ctx, argsB); err != nil {
		t.Fatalf("record call B: %v", err)
	}
	if stub.calls != 2 {
		t.Fatalf("record phase: stub called %d times, want 2", stub.calls)
	}
	if got := a.toolTape.tape.Len(); got != 2 {
		t.Fatalf("record phase: tape has %d entries, want 2", got)
	}
	if _, err := os.Stat(tapePath); err != nil {
		t.Fatalf("tape file not flushed after record: %v", err)
	}

	// --- Replay phase: reload from disk, real tool must never run. ---
	loaded, err := toolreplay.LoadTape(tapePath)
	if err != nil {
		t.Fatalf("LoadTape: %v", err)
	}
	replayStub := &tapeStubTool{name: "read_file", resp: tool.Result{Content: "LIVE SIDE EFFECT"}}
	ra := &Agent{toolTape: &toolTapeState{tape: loaded, mode: toolTapeReplay, path: tapePath}}

	res, err := ra.safeExecute(replayStub, ctx, argsA)
	if err != nil {
		t.Fatalf("replay call A: %v", err)
	}
	if res.Content != "hello world" {
		t.Fatalf("replay call A: content %q, want recorded %q", res.Content, "hello world")
	}
	res, err = ra.safeExecute(replayStub, ctx, argsB)
	if err != nil {
		t.Fatalf("replay call B: %v", err)
	}
	if res.Content != "hello world" {
		t.Fatalf("replay call B: content %q, want recorded %q", res.Content, "hello world")
	}
	if replayStub.calls != 0 {
		t.Fatalf("replay phase: real tool executed %d times, want 0", replayStub.calls)
	}
}

func TestToolTapeReplayMissIsExplicitError(t *testing.T) {
	tape := toolreplay.NewTape()
	stub := &tapeStubTool{name: "read_file", resp: tool.Result{Content: "LIVE"}}
	a := &Agent{toolTape: &toolTapeState{tape: tape, mode: toolTapeReplay, path: "/tmp/missing.tape.json"}}

	res, err := a.safeExecute(stub, context.Background(), json.RawMessage(`{"path":"unrecorded.txt"}`))
	if err != nil {
		t.Fatalf("replay miss: unexpected go-level error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("replay miss: result should be an error result, got %+v", res)
	}
	if !strings.Contains(res.Content, "tape miss") {
		t.Fatalf("replay miss: content %q should explain the miss", res.Content)
	}
	if stub.calls != 0 {
		t.Fatalf("replay miss: real tool was executed %d times, want 0 (no silent fallback)", stub.calls)
	}
}

func TestToolTapeFIFORepeatedInput(t *testing.T) {
	tape := toolreplay.NewTape()
	input := json.RawMessage(`{"cmd":"true"}`)
	for _, content := range []string{"first", "second"} {
		tape.Record(toolreplay.Entry{
			ToolName:  "run_command",
			Input:     input,
			InputHash: toolreplay.HashInput(input),
			Result:    toolreplay.Result{Content: content},
		})
	}
	a := &Agent{toolTape: &toolTapeState{tape: tape, mode: toolTapeReplay, path: "/tmp/fifo.tape.json"}}
	stub := &tapeStubTool{name: "run_command"}
	for i, want := range []string{"first", "second"} {
		res, err := a.safeExecute(stub, context.Background(), input)
		if err != nil {
			t.Fatalf("FIFO call %d: %v", i, err)
		}
		if res.Content != want {
			t.Fatalf("FIFO call %d: content %q, want %q", i, res.Content, want)
		}
	}
	// Third identical call: tape exhausted, explicit miss (never live).
	res, err := a.safeExecute(stub, context.Background(), input)
	if err != nil || !res.IsError || !strings.Contains(res.Content, "tape miss") {
		t.Fatalf("FIFO exhaustion: want explicit miss error, got (%+v, %v)", res, err)
	}
	if stub.calls != 0 {
		t.Fatalf("FIFO exhaustion: real tool executed %d times, want 0", stub.calls)
	}
}

func TestToolTapeCancelNotRecorded(t *testing.T) {
	tape := toolreplay.NewTape()
	a := &Agent{toolTape: &toolTapeState{tape: tape, mode: toolTapeRecord, path: filepath.Join(t.TempDir(), "c.tape.json")}}
	stub := &tapeStubTool{name: "slow_tool"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: safeExecute takes the cancellation path
	res, err := a.safeExecute(stub, ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("cancelled call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("cancelled call: want error result, got %+v", res)
	}
	if tape.Len() != 0 {
		t.Fatalf("cancelled call: tape has %d entries, want 0 (cancellations are not tool facts)", tape.Len())
	}
}

func TestToolTapeGoErrorFidelity(t *testing.T) {
	dir := t.TempDir()
	tapePath := filepath.Join(dir, "e.tape.json")
	ctx := context.Background()

	stub := &tapeStubTool{name: "flaky", err: context.DeadlineExceeded}
	a := &Agent{toolTape: &toolTapeState{tape: toolreplay.NewTape(), mode: toolTapeRecord, path: tapePath}}
	if _, err := a.safeExecute(stub, ctx, json.RawMessage(`{"x":1}`)); err == nil {
		t.Fatalf("record phase: want go-level error propagated, got nil")
	}

	loaded, err := toolreplay.LoadTape(tapePath)
	if err != nil {
		t.Fatalf("LoadTape: %v", err)
	}
	ra := &Agent{toolTape: &toolTapeState{tape: loaded, mode: toolTapeReplay, path: tapePath}}
	replayStub := &tapeStubTool{name: "flaky"}
	if _, err := ra.safeExecute(replayStub, ctx, json.RawMessage(`{"x":1}`)); err == nil {
		t.Fatalf("replay phase: want go-level error reproduced, got nil")
	}
	if replayStub.calls != 0 {
		t.Fatalf("replay phase: real tool executed %d times, want 0", replayStub.calls)
	}
}
