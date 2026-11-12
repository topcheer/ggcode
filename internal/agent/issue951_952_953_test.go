package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newValidationAgent() *Agent {
	return NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
}

func TestEnsureMessagesSendableReordersInBatchGuidance(t *testing.T) {
	a := newValidationAgent()

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("do the task")}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t1", "read_file", []byte(`{"path":"a.go"}`)),
		}},

		{Role: "user", Content: []provider.ContentBlock{
			provider.TextBlock("[Reversibility] think twice before this destructive action"),
		}},
		{Role: "user", Content: []provider.ContentBlock{
			provider.ToolResultNamedBlock("t1", "read_file", "file contents", false),
		}},
		{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("done")}},
	}

	out := a.ensureMessagesSendable(msgs)

	if len(out) != 4 {
		t.Fatalf("expected 4 messages after repair, got %d: %#v", len(out), out)
	}

	if out[1].Role != "assistant" || len(out[1].Content) != 1 || out[1].Content[0].Type != "tool_use" {
		t.Fatalf("out[1] should be the assistant tool_use message, got %#v", out[1])
	}
	if out[2].Role != "user" || len(out[2].Content) == 0 || out[2].Content[0].Type != "tool_result" || out[2].Content[0].ToolID != "t1" {
		t.Fatalf("out[2] must start with the tool_result for t1, got %#v", out[2])
	}

	if len(out[2].Content) != 2 || out[2].Content[1].Type != "text" ||
		!strings.Contains(out[2].Content[1].Text, "[Reversibility]") {
		t.Fatalf("guidance text must follow the tool_result block in out[2], got %#v", out[2])
	}
	if out[3].Role != "assistant" || !strings.Contains(out[3].Content[0].Text, "done") {
		t.Fatalf("out[3] should be the final assistant message, got %#v", out[3])
	}
}

func TestEnsureMessagesSendableFlushesGuidanceWhenNeverClosed(t *testing.T) {
	a := newValidationAgent()

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("hi")}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t1", "read_file", []byte(`{"path":"a.go"}`)),
		}},

		{Role: "user", Content: []provider.ContentBlock{
			provider.TextBlock("[Scope Narrow] you dropped a requirement"),
		}},
	}

	out := a.ensureMessagesSendable(msgs)

	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
	}
	last := out[len(out)-1]
	if last.Role != "user" || len(last.Content) != 2 ||
		last.Content[0].Type != "tool_result" || last.Content[0].ToolID != "t1" ||
		last.Content[1].Type != "text" || !strings.Contains(last.Content[1].Text, "[Scope Narrow]") {
		t.Fatalf("deferred guidance must be flushed after synthetic tool_results, got %#v", last)
	}
}

func TestEnsureMessagesSendableKeepsCleanSequenceUnchanged(t *testing.T) {
	a := newValidationAgent()

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("hi")}},
		{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("t1", "read_file", []byte(`{"path":"a.go"}`)),
		}},
		{Role: "user", Content: []provider.ContentBlock{
			provider.ToolResultNamedBlock("t1", "read_file", "contents", false),
		}},
		{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("done")}},
	}

	out := a.ensureMessagesSendable(msgs)

	if len(out) != len(msgs) {
		t.Fatalf("clean sequence must pass through unchanged, got %d messages: %#v", len(out), out)
	}
	for i := range out {
		if out[i].Role != msgs[i].Role || len(out[i].Content) != len(msgs[i].Content) {
			t.Fatalf("message %d mutated: %#v vs %#v", i, out[i], msgs[i])
		}
	}
}

func TestConsensusRecordFiringsAndCheckOnly(t *testing.T) {
	s := newConsensusState()

	s.recordFiring("Failure Mode", 1)
	s.recordFiring("Error Cascade", 1)
	s.recordFiring("Convergence Lock", 1)

	got := s.checkOnly()
	if got == "" {
		t.Fatal("expected consensus alert after 3 distinct detectors fired, got empty")
	}
	if !strings.Contains(got, "Failure Mode") || !strings.Contains(got, "Error Cascade") {
		t.Fatalf("alert should name the firing detectors, got: %s", got)
	}
}

func TestConsensusLiveDetectorsBelowAndAtThreshold(t *testing.T) {
	s := newConsensusState()

	s.recordFiring("Error Rush", 1)
	s.recordFiring("Tunnel Vision", 1)
	if got := s.checkOnly(); got != "" {

		t.Fatalf("two live tags are below threshold, expected no alert, got: %s", got)
	}

	s.recordFiring("Analysis Paralysis", 1)
	if got := s.checkOnly(); got == "" {
		t.Fatal("three live tags across scans should trigger consensus")
	}
}

func TestTokenWasteBudgetOriginalLenDrivesMetering(t *testing.T) {
	s := newTokenWasteBudgetState()

	polluted := strings.Repeat("x", 100)
	s.recordToolResultLen("read_file", polluted, 10, false, false, nil)

	if len(s.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(s.entries))
	}
	want := estimateTokensLen(polluted, 10)
	if s.entries[0].tokens != want {
		t.Fatalf("metering must use originalLen: want %d tokens, got %d", want, s.entries[0].tokens)
	}
	if want >= estimateTokensLen(polluted, 100) {
		t.Fatalf("sanity: originalLen-derived tokens (%d) should be smaller than polluted (%d)",
			want, estimateTokensLen(polluted, 100))
	}
	if s.entries[0].category != wasteNone {
		t.Fatalf("substantive non-error result must be productive, got %v", s.entries[0].category)
	}
}

func TestAgentGoOriginalContentLenCapturedBeforeDetectorChain(t *testing.T) {
	// #5543: the measuredLen sites moved out of agent.go; scan the
	// stream-loop source files (shape-agnostic file targeting).
	srcText, err := readStreamLoopSource("agent.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}
	captureIdx := strings.Index(srcText, "measuredLen := len(result.Content)")
	applyIdx := strings.Index(srcText, "a.applyToolResultGuidance(")
	shrinkIdx := strings.LastIndex(srcText, "guardToolOutput(result.Content")
	if captureIdx < 0 {
		t.Fatal("measuredLen capture not found in agent.go")
	}
	if applyIdx < 0 || captureIdx > applyIdx {
		t.Fatalf("measuredLen capture (%d) must precede applyToolResultGuidance (%d)", captureIdx, applyIdx)
	}
	if shrinkIdx < 0 || captureIdx < shrinkIdx {
		t.Fatalf("measuredLen capture (%d) must follow the last shrink site (%d) - #1819 case 2", captureIdx, shrinkIdx)
	}
}

func TestSyncVerifyAndGateReturnContract(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)

	shouldContinue, passed := a.syncVerifyAndGate(context.Background(), &RunStats{}, 0)
	if shouldContinue {
		t.Fatal("expected shouldContinue=false when verification cannot run")
	}
	if passed {
		t.Fatal("expected passed=false when verification cannot run (not a pass)")
	}
}

func runFutileCycleScenario(t *testing.T, editPath, oldText, newText string) *Agent {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/src", 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	for _, p := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(dir+"/src/"+p, []byte("package src\n\nconst X = 1\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	registry := tool.NewRegistry()

	if err := registry.Register(mockTool{
		name:   "read_file",
		result: tool.Result{Content: "package main\n\nfunc main() {}"},
	}); err != nil {
		t.Fatalf("register read_file: %v", err)
	}

	if err := registry.Register(mockTool{name: "edit_file"}); err != nil {
		t.Fatalf("register edit_file: %v", err)
	}

	reads := func(prefix string) []provider.ContentBlock {
		var blocks []provider.ContentBlock
		for _, p := range []string{"src/a.go", "src/b.go", "src/c.go", "src/d.go"} {
			blocks = append(blocks, provider.ToolUseBlock(prefix+p, "read_file",
				[]byte(`{"path":"`+p+`"}`)))
		}
		return blocks
	}
	editJSON := `{"file_path":"` + filepath.Join(dir, editPath) +
		`","old_text":"` + oldText + `","new_text":"` + newText + `"}`
	edit := provider.ToolUseBlock("edit_1", "edit_file", []byte(editJSON))

	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{Role: "assistant", Content: reads("r1-")}},
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{edit}}},
			{Message: provider.Message{Role: "assistant", Content: reads("r2-")}},
			{Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("done")}}},
		},
	}

	a := NewAgent(mp, registry, dir, 10)
	var events []provider.StreamEvent
	if err := a.RunStream(context.Background(), "fix it", func(ev provider.StreamEvent) {
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			for _, ev := range events {
				t.Logf("event: type=%d tool=%q text=%q result=%q", int(ev.Type), ev.Tool.Name, ev.Text, ev.Result)
			}
		}
	})
	return a
}

func TestFutileCycleFailedEditDoesNotResetEpoch(t *testing.T) {

	a := runFutileCycleScenario(t, "src/missing.go", "A", "B")

	if len(a.futileCycle.pastEpochs) != 0 {
		t.Fatalf("failed edit must not finalize an epoch, got %d past epochs", len(a.futileCycle.pastEpochs))
	}
	if a.futileCycle.warningsFired != 0 {
		t.Fatalf("failed edit + legitimate re-reads must not fire futile-cycle warnings, got %d", a.futileCycle.warningsFired)
	}
}

func TestFutileCycleSuccessfulEditStillDetectsRevisit(t *testing.T) {

	a := runFutileCycleScenario(t, "src/a.go", "const X = 1", "const X = 2")

	if len(a.futileCycle.pastEpochs) == 0 {
		t.Fatalf("successful edit must finalize the read epoch (currentEpoch=%d, warnings=%d)",
			len(a.futileCycle.currentEpoch), a.futileCycle.warningsFired)
	}
	if a.futileCycle.warningsFired == 0 {
		t.Fatal("identical re-read set after a successful edit must fire the futile-cycle warning")
	}
}
