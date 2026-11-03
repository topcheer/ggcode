package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue535_ReconcilePreservesUserBlocksInLateResultMessage(t *testing.T) {
	cm := NewManager(100000)

	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "tool_use", ToolID: "call_A", ToolName: "bash"},
	}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "still working"},
	}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "call_A", Output: "late result"},
		{Type: "text", Text: "IMPORTANT user note"},
		{Type: "image", ImageMIME: "image/png", ImageData: "aGVsbG8="},
	}})

	if !cm.ReconcileToolCalls() {
		t.Fatal("expected ReconcileToolCalls to report changes")
	}

	msgs := cm.Messages()
	var userText, userImage, lateResult bool
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Text == "IMPORTANT user note" {
				userText = true
			}
			if b.Type == "image" && b.ImageData == "aGVsbG8=" {
				userImage = true
			}
			if b.Type == "tool_result" && b.ToolID == "call_A" {
				lateResult = true
			}
		}
	}
	if !userText {
		t.Error("user text block was lost by ReconcileToolCalls (Bug C)")
	}
	if !userImage {
		t.Error("user image block was lost by ReconcileToolCalls (Bug C)")
	}
	if !lateResult {
		t.Error("late tool_result should still be present (relocated)")
	}

	for i, msg := range msgs {
		if msg.Role == "assistant" && len(msg.Content) > 0 && msg.Content[0].Text == "still working" {
			for _, prev := range msgs[:i] {
				for _, b := range prev.Content {
					if b.Type == "tool_result" && b.ToolID == "call_A" {
						return
					}
				}
			}
			t.Error("relocated tool_result not placed before next assistant message")
		}
	}
}

func TestIssue535_ReconcileDropsPureToolResultMessage(t *testing.T) {
	cm := NewManager(100000)
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "tool_use", ToolID: "call_1", ToolName: "bash"},
	}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "boundary"},
	}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "call_1", Output: "out"},
	}})

	if !cm.ReconcileToolCalls() {
		t.Fatal("expected ReconcileToolCalls to report changes")
	}

	msgs := cm.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after reconcile, got %d: %+v", len(msgs), msgs)
	}
	count := 0
	for _, msg := range msgs {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "call_1" {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 relocated tool_result, got %d", count)
	}
}

func TestIssue535_TokenizerCyrillicNotPricedAsCJK(t *testing.T) {
	cyr := strings.Repeat("д", 1000)
	if got := EstimateTokens(cyr); got != 401 {
		t.Errorf("EstimateTokens(1000 Cyrillic) = %d, want 401 (2.5 chars/token)", got)
	}
	if got := EstimateTokens(cyr); float64(got) > 500 {
		t.Errorf("Cyrillic estimate %d exceeds 0.5 token/char floor", got)
	}
	if got := EstimateTokens(cyr); got >= 1000 {
		t.Errorf("Cyrillic still priced as CJK: got %d, want << 1000", got)
	}
}

func TestIssue535_TokenizerLatinExtendedTier(t *testing.T) {
	acc := strings.Repeat("é", 200)
	if got := EstimateTokens(acc); got != 67 {
		t.Errorf("EstimateTokens(200 Latin-ext) = %d, want 67 (3.0 chars/token)", got)
	}
	fr := strings.Repeat("a", 800) + strings.Repeat("é", 200)
	if got := EstimateTokens(fr); got != 295 {
		t.Errorf("EstimateTokens(800 ASCII + 200 é) = %d, want 295", got)
	}
}

func TestIssue535_TokenizerGreekTier(t *testing.T) {
	el := strings.Repeat("α", 500)
	if got := EstimateTokens(el); got != 251 {
		t.Errorf("EstimateTokens(500 Greek) = %d, want 251 (2.0 chars/token)", got)
	}
}

func TestIssue535_TokenizerCJKSemanticsUnchanged(t *testing.T) {
	if got := EstimateTokens("你好"); got != 3 {
		t.Errorf("EstimateTokens(你好) = %d, want 3 (#515: 1 token/char + 1)", got)
	}
	if got := EstimateTokens("你好世界"); got != 5 {
		t.Errorf("EstimateTokens(你好世界) = %d, want 5", got)
	}
	if got := EstimateTokens("hello你好"); got != 4 {
		t.Errorf("EstimateTokens(hello你好) = %d, want 4", got)
	}
	if got := EstimateTokens("한글"); got != 3 {
		t.Errorf("EstimateTokens(한글) = %d, want 3", got)
	}
	if got := EstimateTokens("hello world"); got != 4 {
		t.Errorf("EstimateTokens(ascii) = %d, want 4", got)
	}
}

func TestIssue535_TokenizerEmojiKeepsConservativeTier(t *testing.T) {
	e := strings.Repeat("🙂", 50)
	if got := EstimateTokens(e); got != 51 {
		t.Errorf("EstimateTokens(50 emoji) = %d, want 51 (1.0 chars/token)", got)
	}
}

func TestIssue535_EstimateTokensCalibratedScriptTiers(t *testing.T) {
	c := NewTokenCalibrator()
	if got := EstimateTokensCalibrated(strings.Repeat("д", 1000), c); got != 401 {
		t.Errorf("EstimateTokensCalibrated(1000 Cyrillic) = %d, want 401", got)
	}
	if got := EstimateTokensCalibrated(strings.Repeat("a", 800), c); got != 229 {
		t.Errorf("EstimateTokensCalibrated(800 ascii) = %d, want 229", got)
	}
	if got := EstimateTokensCalibrated(strings.Repeat("д", 100), nil); got != 41 {
		t.Errorf("EstimateTokensCalibrated(nil cal) = %d, want 41", got)
	}
}

func TestIssue535_EstimateMessagesTokensCountsReasoningAndToolUse(t *testing.T) {
	reasoning := strings.Repeat("r", 10000)
	content := []provider.ContentBlock{{Type: "text", Text: reasoning}}
	for i := 0; i < 10; i++ {
		content = append(content, provider.ContentBlock{
			Type:     "tool_use",
			ToolID:   "call",
			ToolName: "read_file",
			Input:    []byte(`{"path":"/a.go"}`),
		})
	}
	msg := provider.Message{Role: "assistant", Content: content}

	sliceEstimate := estimateMessagesTokens([]provider.Message{msg})
	perMsgEstimate := estimateTokensStandalone(msg)

	if sliceEstimate != perMsgEstimate {
		t.Errorf("estimateMessagesTokens (%d) != estimateTokens (%d) — accounting paths diverged (Bug B)",
			sliceEstimate, perMsgEstimate)
	}
	if sliceEstimate < 2858 {
		t.Errorf("estimateMessagesTokens = %d, want ≥ 2858 (10K reasoning chars must be counted)", sliceEstimate)
	}
	if sliceEstimate < 2920 {
		t.Errorf("estimateMessagesTokens = %d, tool_use ×6 overhead appears missing", sliceEstimate)
	}
}

func TestIssue535_EstimateMessagesTokensReasoningSensitivity(t *testing.T) {
	plain := []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "answer"},
	}}}
	withReasoning := []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "answer"},
		{Type: "text", Text: "", ReasoningContent: strings.Repeat("think ", 1000)},
	}}}
	a, b := estimateMessagesTokens(plain), estimateMessagesTokens(withReasoning)
	if b <= a {
		t.Errorf("reasoning content invisible to estimateMessagesTokens: plain=%d withReasoning=%d", a, b)
	}
	if b-a < 1700 {
		t.Errorf("reasoning undercounted: delta=%d, want ≥1700 (6000 chars @3.5)", b-a)
	}
}
