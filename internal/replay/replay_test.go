package replay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// fakeTool is a minimal read-only Tool for replay tests.
type fakeTool struct {
	name string
	out  string
	err  bool
}

func (f fakeTool) Name() string        { return f.name }
func (f fakeTool) Description() string { return "fake " + f.name }
func (f fakeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (f fakeTool) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: f.out, IsError: f.err}, nil
}

func newTestRegistry(tools ...fakeTool) *tool.Registry {
	reg := tool.NewRegistry()
	for _, t := range tools {
		_ = reg.Register(t)
	}
	return reg
}

func msg(role string, blocks ...provider.ContentBlock) provider.Message {
	return provider.Message{Role: role, Content: blocks}
}

func TestExtractTracePairsToolUseAndResult(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.TextBlock("do it")),
		msg("assistant",
			provider.TextBlock("thinking"),
			provider.ToolUseBlock("call_1", "read_file", json.RawMessage(`{"path":"a.go"}`)),
			provider.ToolUseBlock("call_2", "grep", json.RawMessage(`{"pattern":"foo"}`)),
		),
		msg("user",
			provider.ToolResultBlock("call_1", "contents of a.go", false),
			provider.ToolResultBlock("call_2", "foo matches", false),
		),
	}
	steps := ExtractTrace(msgs)
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[0].ToolName != "read_file" || steps[0].RecordedOutput != "contents of a.go" {
		t.Errorf("step 0 mismatch: %+v", steps[0])
	}
	if steps[1].ToolName != "grep" || steps[1].Unpaired {
		t.Errorf("step 1 mismatch: %+v", steps[1])
	}
	if steps[0].Index != 1 || steps[1].Index != 2 {
		t.Errorf("indices not sequential: %d, %d", steps[0].Index, steps[1].Index)
	}
}

func TestExtractTraceUnpairedCall(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", provider.ToolUseBlock("call_x", "read_file", nil)),
	}
	steps := ExtractTrace(msgs)
	if len(steps) != 1 || !steps[0].Unpaired {
		t.Fatalf("want single unpaired step, got %+v", steps)
	}
}

func TestReplayMatchAndDrift(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant",
			provider.ToolUseBlock("c1", "fake_ro", json.RawMessage(`{"path":"a"}`)),
			provider.ToolUseBlock("c2", "fake_ro", json.RawMessage(`{"path":"b"}`)),
		),
		msg("user",
			provider.ToolResultBlock("c1", "line1\nline2", false),
			provider.ToolResultBlock("c2", "old fact\nline2", false),
		),
	}
	trace := ExtractTrace(msgs)
	reg := newTestRegistry(
		fakeTool{name: "fake_ro", out: "line1\nline2"},
	)
	// fake_ro is not on the default allowlist; override for the test.
	rp := NewReplayer(reg)
	rp.AllowedTools = map[string]bool{"fake_ro": true}
	rep := rp.Run(context.Background(), "sess", trace)
	if len(rep.Steps) != 2 {
		t.Fatalf("want 2 step results, got %d", len(rep.Steps))
	}
	if rep.Steps[0].Status != StatusMatch {
		t.Errorf("step 0: want match, got %s", rep.Steps[0].Status)
	}
	if rep.Steps[1].Status != StatusDrift {
		t.Errorf("step 1: want drift, got %s", rep.Steps[1].Status)
	}
	if rep.Steps[1].RemovedLines != 1 || rep.Steps[1].AddedLines != 1 {
		t.Errorf("step 1 diff stats wrong: added=%d removed=%d", rep.Steps[1].AddedLines, rep.Steps[1].RemovedLines)
	}
	if rep.Steps[1].FirstDiffLine != 1 {
		t.Errorf("step 1 first diff line: want 1, got %d", rep.Steps[1].FirstDiffLine)
	}
	if !rep.DriftDetected() {
		t.Error("DriftDetected() = false, want true")
	}
}

func TestReplaySkipsMutatingAndMissingTools(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant",
			provider.ToolUseBlock("c1", "edit_file", json.RawMessage(`{"path":"a"}`)),
			provider.ToolUseBlock("c2", "gone_tool", json.RawMessage(`{}`)),
		),
		msg("user",
			provider.ToolResultBlock("c1", "edited", false),
			provider.ToolResultBlock("c2", "ok", false),
		),
	}
	trace := ExtractTrace(msgs)
	rp := NewReplayer(newTestRegistry()) // empty registry
	rep := rp.Run(context.Background(), "sess", trace)
	if rep.Steps[0].Status != StatusSkipMutating {
		t.Errorf("edit_file: want skip-mutating, got %s", rep.Steps[0].Status)
	}
	if rep.Steps[1].Status != StatusSkipMutating {
		t.Errorf("unregistered non-allowlisted tool: want skip-mutating, got %s", rep.Steps[1].Status)
	}
	if rep.DriftDetected() {
		t.Error("skips must not count as drift")
	}
}

func TestReplayAllowlistedButMissingTool(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", provider.ToolUseBlock("c1", "grep", json.RawMessage(`{"pattern":"x"}`))),
		msg("user", provider.ToolResultBlock("c1", "x", false)),
	}
	trace := ExtractTrace(msgs)
	rp := NewReplayer(newTestRegistry()) // grep not registered
	rep := rp.Run(context.Background(), "sess", trace)
	if rep.Steps[0].Status != StatusSkipMissingTool {
		t.Errorf("want skip-missing, got %s", rep.Steps[0].Status)
	}
}

func TestReplayErrorFlagFlipIsDrift(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", provider.ToolUseBlock("c1", "fake_ro", json.RawMessage(`{}`))),
		msg("user", provider.ToolResultBlock("c1", "same text", false)),
	}
	trace := ExtractTrace(msgs)
	// Live execution errors where the recorded one succeeded (or text equal
	// but error flag differs) -- must count as drift, not match.
	rp := NewReplayer(newTestRegistry(fakeTool{name: "fake_ro", out: "same text", err: true}))
	rp.AllowedTools = map[string]bool{"fake_ro": true}
	rep := rp.Run(context.Background(), "sess", trace)
	if rep.Steps[0].Status != StatusDrift {
		t.Errorf("error-flag flip: want drift, got %s", rep.Steps[0].Status)
	}
}

func TestReplayExecErrorRecorded(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", provider.ToolUseBlock("c1", "fake_ro", json.RawMessage(`{}`))),
		msg("user", provider.ToolResultBlock("c1", "x", true)),
	}
	trace := ExtractTrace(msgs)
	rp := NewReplayer(newTestRegistry(fakeTool{name: "fake_ro", out: "x", err: true}))
	rp.AllowedTools = map[string]bool{"fake_ro": true}
	rep := rp.Run(context.Background(), "sess", trace)
	if rep.Steps[0].Status != StatusMatch {
		t.Errorf("matching error result: want match, got %s", rep.Steps[0].Status)
	}
}

func TestNormalizeNoiseTiers(t *testing.T) {
	a := "line1\nline2  \n"
	b := "line1\r\nline2\n\n\n"
	if normalize(a) != normalize(b) {
		t.Errorf("cosmetic-only difference counted as drift:\n%q\n%q", normalize(a), normalize(b))
	}
}

func TestDiffStatsFirstDiffLine(t *testing.T) {
	added, removed, first := diffStats("a\nb\nc", "a\nX\nc")
	if added != 1 || removed != 1 || first != 2 {
		t.Errorf("want +1/-1 first=2, got +%d/-%d first=%d", added, removed, first)
	}
	if a, r, f := diffStats("same\nlines", "same\nlines"); a != 0 || r != 0 || f != 0 {
		t.Errorf("equal outputs should yield 0/0/0, got +%d/-%d first=%d", a, r, f)
	}
}

func TestReportRenderSmoke(t *testing.T) {
	rep := &Report{SessionID: "s1", Steps: []StepResult{
		{Step: TraceStep{Index: 1, ToolName: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}, Status: StatusMatch},
		{Step: TraceStep{Index: 2, ToolName: "grep", Input: json.RawMessage(`{"pattern":"todo"}`)}, Status: StatusDrift, AddedLines: 2, RemovedLines: 1, FirstDiffLine: 3},
	}}
	var sb strings.Builder
	rep.Render(&sb)
	out := sb.String()
	if !strings.Contains(out, "replay s1: 2 steps -- 1 match, 1 drift, 0 skipped") {
		t.Errorf("summary line wrong:\n%s", out)
	}
	if !strings.Contains(out, "DRIFT") || !strings.Contains(out, "path=a.go") {
		t.Errorf("missing detail lines:\n%s", out)
	}
}
