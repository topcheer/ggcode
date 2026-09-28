package agent

// r214 pin tests (track B behavior-preserving split of RunStreamWithContent,
// round 3: no-tool-calls completion block → finishNoToolTurnPhase seam).
// The block has 22 exits: 19 `continue` (next iteration) + 3 `return nil`
// (run finished). These pins lock the exits whose observable behavior an
// extract-method translation could silently flip (the r213 lesson: control-
// flow translation is the dominant risk):
//   - final fall-through `return nil` (all gates passed → run done)
//   - truncation recovery `continue` (#677 continuation protocol)
//   - inline tool-call nudge `continue`
// The remaining exits are covered by existing suites: autopilot strategist
// paths (deadlock/complete/empty-guidance/budget) by the
// TestRunStreamAutopilot* family, the empty-response nudge by
// TestPinR213EmptyResponseNudgeSequence, and the advisory detectors fire
// inside every pin run here as side effects.

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestPinR214NoToolCallsFallThrough locks the final fall-through exit: a
// plain-text response with no tool calls and no gate findings must return
// nil from RunStreamWithContent and relay the text exactly once.
func TestPinR214NoToolCallsFallThrough(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("all done, nothing pending")},
			}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 3)

	var texts []string
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventText {
				texts = append(texts, ev.Text)
			}
		})
	if err != nil {
		t.Fatalf("expected nil error on plain completion, got %v", err)
	}
	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "all done, nothing pending") {
		t.Fatalf("expected final text relayed, got %q", texts)
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.capturedMsgs) != 1 {
		t.Fatalf("expected exactly 1 LLM request (no gate continue), got %d", len(mp.capturedMsgs))
	}
}

// TestPinR214TruncationAutoContinue locks the truncation-recovery exit: a
// Done event with Truncated=true must add the partial assistant message,
// inject the #677 continuation prompt (direct add, not budget-routed), and
// continue the loop; the follow-up request must carry that prompt, and the
// run finishes with the second response's text.
func TestPinR214TruncationAutoContinue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventText, Text: "partial answer"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}, Truncated: true},
			},
		},
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("continued answer")},
			}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 3)

	var texts []string
	var systemTexts []string
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			switch ev.Type {
			case provider.StreamEventText:
				texts = append(texts, ev.Text)
			case provider.StreamEventSystem:
				systemTexts = append(systemTexts, ev.Text)
			}
		})
	if err != nil {
		t.Fatalf("expected nil error after truncation recovery, got %v", err)
	}
	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "partial answer") || !strings.Contains(joined, "continued answer") {
		t.Fatalf("expected partial + continued text relayed, got %q", texts)
	}
	sys := strings.Join(systemTexts, "")
	if !strings.Contains(sys, "Response was truncated by output length limit") {
		t.Fatalf("expected truncation system notice, got %q", systemTexts)
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.capturedMsgs) != 2 {
		t.Fatalf("expected 2 LLM requests (truncation continue + finish), got %d", len(mp.capturedMsgs))
	}
	contPrompt := "Your previous response was cut off by the output token limit. Continue from where you left off — do not repeat what you already wrote."
	found := false
	for _, msg := range mp.capturedMsgs[1] {
		for _, b := range msg.Content {
			if b.Type == "text" && b.Text == contPrompt {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("continuation prompt missing from follow-up request: %+v", mp.capturedMsgs[1])
	}
}

// TestPinR214InlineToolCallNudge locks the inline-tool-call nudge exit: a
// text response embedding a prose tool call must increment the nudge cap,
// add the assistant message plus the format-correction prompt, and continue;
// the follow-up request must carry the nudge text.
func TestPinR214InlineToolCallNudge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{provider.TextBlock(
					`I will now read the file: {"name":"read_file","arguments":{"path":"main.go"}} as planned here.`)},
			}},
			{Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{provider.TextBlock("done properly")},
			}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 3)

	var texts []string
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "pin prompt"}},
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventText {
				texts = append(texts, ev.Text)
			}
		})
	if err != nil {
		t.Fatalf("expected nil error after inline nudge recovery, got %v", err)
	}
	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "done properly") {
		t.Fatalf("expected second-response text relayed, got %q", texts)
	}
	mp.mu.Lock()
	defer mp.mu.Unlock()
	if len(mp.capturedMsgs) != 2 {
		t.Fatalf("expected 2 LLM requests (nudge continue + finish), got %d", len(mp.capturedMsgs))
	}
	nudge := "Use structured tool_use format, not inline text syntax for tool calls."
	found := false
	for _, msg := range mp.capturedMsgs[1] {
		for _, b := range msg.Content {
			if b.Type == "text" && b.Text == nudge {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("inline tool-call nudge missing from follow-up request: %+v", mp.capturedMsgs[1])
	}
}
