package agent

// Companion tests for issues #951, #952, #953.
//
// #951 (protocol-level run abort): batch-loop detectors inject pure-text user
// guidance between an open assistant(tool_use) message and its closing
// user(tool_result) message. ensureMessagesSendable now defers that guidance
// and re-appends it AFTER the tool_result blocks, restoring a legal sequence.
//
// #952 (metering bugs): (a) originalContentLen is captured BEFORE the detector
// chain so guidance never inflates token-waste metering; (b) cross-detector
// consensus uses explicit recordFiring calls (checkOnly) instead of the fragile
// baseline-offset content scan; dead scan tags removed.
//
// #953 (detector defects): (a) syncVerifyAndGate reports an explicit `passed`
// flag so retry-round passes skip redundant async verify; (b) a FAILED edit no
// longer resets the futile-cycle epoch.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// --- #951: batch-injected guidance reordering ---

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
		// Illegal injection (#951): detector guidance added mid-batch, before
		// the batch's tool_results are enqueued.
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
	// The assistant(tool_use) message must be IMMEDIATELY followed by a user
	// message containing the matching tool_result — no pure-text user message
	// in between (strict providers reject that with a non-retryable 400).
	if out[1].Role != "assistant" || len(out[1].Content) != 1 || out[1].Content[0].Type != "tool_use" {
		t.Fatalf("out[1] should be the assistant tool_use message, got %#v", out[1])
	}
	if out[2].Role != "user" || len(out[2].Content) == 0 || out[2].Content[0].Type != "tool_result" || out[2].Content[0].ToolID != "t1" {
		t.Fatalf("out[2] must start with the tool_result for t1, got %#v", out[2])
	}
	// The deferred guidance text must be merged into that same user message,
	// AFTER the tool_result block.
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
		// Guidance injected mid-batch, and the conversation ends before the
		// tool_result message ever arrives.
		{Role: "user", Content: []provider.ContentBlock{
			provider.TextBlock("[Scope Narrow] you dropped a requirement"),
		}},
	}

	out := a.ensureMessagesSendable(msgs)

	// Expected: synthetic tool_result closes t1, with the guidance text
	// merged after the tool_result block in that same message.
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

// --- #952: consensus explicit firing + dead tag cleanup ---

func TestConsensusRecordFiringsAndCheckOnly(t *testing.T) {
	s := newConsensusState()

	// Simulate detectors recording their firings explicitly (#952). All three
	// land on the same step, mirroring the batch-loop pattern where several
	// detectors fire for one tool result.
	s.recordFiring("Failure Mode")
	s.recordFiring("Error Cascade")
	s.recordFiring("Convergence Lock")

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

	// #952 follow-up: scanAndCheck and its dead-tag list were removed;
	// live detectors record explicitly. Raw content can no longer record
	// firings at all, so the dead-tag concern is moot.

	// Live detectors still work through the explicit recordFiring path.
	s.recordFiring("Error Rush")
	s.recordFiring("Tunnel Vision")
	if got := s.checkOnly(); got != "" {
		// Two detectors is below the threshold of 3 — no alert expected.
		t.Fatalf("two live tags are below threshold, expected no alert, got: %s", got)
	}
	// Third firing crosses the threshold of 3 distinct detectors within the
	// window; the alert is returned by this checkOnly call itself (it fires
	// the alert and enters cooldown).
	s.recordFiring("Analysis Paralysis")
	if got := s.checkOnly(); got == "" {
		t.Fatal("three live tags across scans should trigger consensus")
	}
}

// --- #952-1: token-waste metering uses originalLen, immune to guidance pollution ---

func TestTokenWasteBudgetOriginalLenDrivesMetering(t *testing.T) {
	s := newTokenWasteBudgetState()

	// Simulate what the agent loop does AFTER the #952-1 fix: content carries
	// appended detector guidance (100 chars), but originalLen (10) is what the
	// loop captured BEFORE the detector chain.
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

// TestAgentGoOriginalContentLenCapturedBeforeDetectorChain pins the #952-1 fix
// point: the capture must precede the first detector call in the chain, so
// appended guidance never lands in the metering window.
func TestAgentGoOriginalContentLenCapturedBeforeDetectorChain(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	captureIdx := strings.Index(string(src), "originalContentLen := len(result.Content)")
	classifierIdx := strings.Index(string(src), "a.errorClassifier.classifyToolError(tc.Name, result.Content)")
	if captureIdx < 0 {
		t.Fatal("originalContentLen capture not found in agent.go")
	}
	if classifierIdx < 0 {
		t.Fatal("detector-chain entry point (errorClassifier) not found in agent.go")
	}
	if captureIdx > classifierIdx {
		t.Fatalf("originalContentLen capture (%d) must precede the detector chain entry (%d) — #553/#952 regression", captureIdx, classifierIdx)
	}
}

// --- #953-1: syncVerifyAndGate return contract ---

func TestSyncVerifyAndGateReturnContract(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)

	// No working directory, no code changes: the gate must decline to run and
	// must NOT report a pass. Verifies the new two-value contract
	// (shouldContinue, passed) that the agent loop now destructures.
	shouldContinue, passed := a.syncVerifyAndGate(context.Background(), &RunStats{}, 0)
	if shouldContinue {
		t.Fatal("expected shouldContinue=false when verification cannot run")
	}
	if passed {
		t.Fatal("expected passed=false when verification cannot run (not a pass)")
	}
}

// --- #953-2: failed edit must not reset the futile-cycle epoch ---

func runFutileCycleScenario(t *testing.T, editPath, oldText, newText string) *Agent {
	t.Helper()

	// Real temp workspace: the agent's executeFileTool wrapper intercepts
	// edit_file and reads the target file before delegating, so a successful
	// edit requires the file to actually exist in the working directory.
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
	// read_file mock: productive read results.
	if err := registry.Register(mockTool{
		name:   "read_file",
		result: tool.Result{Content: "package main\n\nfunc main() {}"},
	}); err != nil {
		t.Fatalf("register read_file: %v", err)
	}
	// edit_file: intercepted by executeFileTool (diff preview + checkpointing),
	// so no mock result is needed; success/failure comes from the real args.
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
	// Target file does not exist: computeFileChange fails, the edit result is
	// an error, no state mutation happened. (Real edit_file arg key is
	// "file_path"; computeFileChange reads the file directly.)
	a := runFutileCycleScenario(t, "src/missing.go", "A", "B")

	// A FAILED edit mutates nothing: the epoch must stay open (no write
	// boundary), so the anchor-recovery re-reads are part of the SAME epoch
	// and no epoch-pair comparison (and no false "[futile-cycle]" warning)
	// can fire.
	if len(a.futileCycle.pastEpochs) != 0 {
		t.Fatalf("failed edit must not finalize an epoch, got %d past epochs", len(a.futileCycle.pastEpochs))
	}
	if a.futileCycle.warningsFired != 0 {
		t.Fatalf("failed edit + legitimate re-reads must not fire futile-cycle warnings, got %d", a.futileCycle.warningsFired)
	}
}

func TestFutileCycleSuccessfulEditStillDetectsRevisit(t *testing.T) {
	// Real file with matching old_text: the edit succeeds and finalizes the
	// read epoch.
	a := runFutileCycleScenario(t, "src/a.go", "const X = 1", "const X = 2")

	// Control: a SUCCESSFUL edit finalizes the epoch; re-reading the identical
	// file set afterwards is a genuine futile cycle and must still be caught.
	if len(a.futileCycle.pastEpochs) == 0 {
		t.Fatalf("successful edit must finalize the read epoch (currentEpoch=%d, warnings=%d)",
			len(a.futileCycle.currentEpoch), a.futileCycle.warningsFired)
	}
	if a.futileCycle.warningsFired == 0 {
		t.Fatal("identical re-read set after a successful edit must fire the futile-cycle warning")
	}
}
