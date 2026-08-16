package context

// Feature tests for issue #535 (context package batch):
//   - Bug C: ReconcileToolCalls drops the whole user message containing a
//     late tool_result — same-message user text/image blocks were silently
//     lost. Fixed by block-level stale granularity.
//   - Bug A: tokenizer priced every rune > 127 at the CJK ratio (1.0
//     chars/token) — Cyrillic/Greek/Latin-extended sessions overestimated
//     up to 2.5x, triggering premature auto-compact. Fixed by script-class
//     tiers (Cyrillic 2.5, Greek 2.0, Latin-ext 3.0 chars/token).
//   - Bug B: estimateMessagesTokens skipped ReasoningContent and the
//     tool_use ×6 overhead — reasoning-heavy recent groups were under-budgeted
//     up to 30x, causing compaction loops. Fixed by sharing accounting with
//     estimateTokens.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// ── Bug C: user content survives ReconcileToolCalls ──

// TestIssue535_ReconcilePreservesUserBlocksInLateResultMessage reproduces the
// issue's probe scenario: a late tool_result shares its user message with a
// text block and an image block. Before the fix both user blocks were
// silently dropped; after the fix only the tool_result block is relocated.
func TestIssue535_ReconcilePreservesUserBlocksInLateResultMessage(t *testing.T) {
	cm := NewManager(100000)

	// [0] assistant with a tool_use
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "tool_use", ToolID: "call_A", ToolName: "bash"},
	}})
	// [1] next assistant boundary (result never arrived before it)
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "still working"},
	}})
	// [2] user message containing the LATE tool_result PLUS real user content
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

	// The relocated tool_result must now sit BEFORE the second assistant msg.
	for i, msg := range msgs {
		if msg.Role == "assistant" && len(msg.Content) > 0 && msg.Content[0].Text == "still working" {
			for _, prev := range msgs[:i] {
				for _, b := range prev.Content {
					if b.Type == "tool_result" && b.ToolID == "call_A" {
						return // correctly relocated before the boundary
					}
				}
			}
			t.Error("relocated tool_result not placed before next assistant message")
		}
	}
}

// TestIssue535_ReconcileDropsPureToolResultMessage verifies a user message
// consisting ONLY of late tool_result blocks is still removed entirely (no
// empty husk left behind).
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

	// Expected shape: [assistant(tool_use), user(relocated tool_result),
	// assistant("boundary")] — the original pure-tool_result message at the
	// tail must be gone entirely (no empty husk).
	msgs := cm.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after reconcile, got %d: %+v", len(msgs), msgs)
	}
	// Exactly one tool_result for call_1 must remain (the relocated one),
	// and it must sit before the assistant boundary message.
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

// ── Bug A: script-classified tokenizer tiers ──

func TestIssue535_TokenizerCyrillicNotPricedAsCJK(t *testing.T) {
	// 1000 Cyrillic chars: real-world ≈ 400 tokens. Old code: 1001 (2.5x over).
	// New tier: 1000/2.5 + 1 = 401.
	cyr := strings.Repeat("д", 1000)
	if got := EstimateTokens(cyr); got != 401 {
		t.Errorf("EstimateTokens(1000 Cyrillic) = %d, want 401 (2.5 chars/token)", got)
	}
	// Must not exceed 0.5 token/char (issue floor).
	if got := EstimateTokens(cyr); float64(got) > 500 {
		t.Errorf("Cyrillic estimate %d exceeds 0.5 token/char floor", got)
	}
	// And must be comfortably below the old CJK pricing of 1001.
	if got := EstimateTokens(cyr); got >= 1000 {
		t.Errorf("Cyrillic still priced as CJK: got %d, want << 1000", got)
	}
}

func TestIssue535_TokenizerLatinExtendedTier(t *testing.T) {
	// 200 accented Latin chars: 200/3.0 = 66 (truncated) + 1 = 67 tokens (old: 201).
	acc := strings.Repeat("é", 200)
	if got := EstimateTokens(acc); got != 67 {
		t.Errorf("EstimateTokens(200 Latin-ext) = %d, want 67 (3.0 chars/token)", got)
	}
	// French mix from the issue: 800 ASCII + 200 accents.
	// ascii 800/3.5=228, latinExt 200/3.0=66 → 228+66+1=295 (old: 429).
	fr := strings.Repeat("a", 800) + strings.Repeat("é", 200)
	if got := EstimateTokens(fr); got != 295 {
		t.Errorf("EstimateTokens(800 ASCII + 200 é) = %d, want 295", got)
	}
}

func TestIssue535_TokenizerGreekTier(t *testing.T) {
	// 500 Greek chars: 500/2.0 + 1 = 251 (old: 501).
	el := strings.Repeat("α", 500)
	if got := EstimateTokens(el); got != 251 {
		t.Errorf("EstimateTokens(500 Greek) = %d, want 251 (2.0 chars/token)", got)
	}
}

func TestIssue535_TokenizerCJKSemanticsUnchanged(t *testing.T) {
	// #515 semantics must hold: CJK stays 1.0 chars/token.
	if got := EstimateTokens("你好"); got != 3 {
		t.Errorf("EstimateTokens(你好) = %d, want 3 (#515: 1 token/char + 1)", got)
	}
	if got := EstimateTokens("你好世界"); got != 5 {
		t.Errorf("EstimateTokens(你好世界) = %d, want 5", got)
	}
	if got := EstimateTokens("hello你好"); got != 4 {
		t.Errorf("EstimateTokens(hello你好) = %d, want 4", got)
	}
	// Hangul also stays in the CJK tier.
	if got := EstimateTokens("한글"); got != 3 {
		t.Errorf("EstimateTokens(한글) = %d, want 3", got)
	}
	// ASCII fast path unchanged.
	if got := EstimateTokens("hello world"); got != 4 {
		t.Errorf("EstimateTokens(ascii) = %d, want 4", got)
	}
}

func TestIssue535_TokenizerEmojiKeepsConservativeTier(t *testing.T) {
	// Emoji / unknown high-bit runes keep the conservative 1.0 chars/token
	// pricing (pre-#535 behavior for the "other" bucket).
	e := strings.Repeat("🙂", 50)
	if got := EstimateTokens(e); got != 51 {
		t.Errorf("EstimateTokens(50 emoji) = %d, want 51 (1.0 chars/token)", got)
	}
}

func TestIssue535_EstimateTokensCalibratedScriptTiers(t *testing.T) {
	c := NewTokenCalibrator()
	// Calibrated path: pure Cyrillic uses only fixed tiers → 401.
	if got := EstimateTokensCalibrated(strings.Repeat("д", 1000), c); got != 401 {
		t.Errorf("EstimateTokensCalibrated(1000 Cyrillic) = %d, want 401", got)
	}
	// Pure ASCII still uses calibrated ratio (default 3.5): 800/3.5+1=229.
	if got := EstimateTokensCalibrated(strings.Repeat("a", 800), c); got != 229 {
		t.Errorf("EstimateTokensCalibrated(800 ascii) = %d, want 229", got)
	}
	// nil calibrator falls back to defaults.
	if got := EstimateTokensCalibrated(strings.Repeat("д", 100), nil); got != 41 {
		t.Errorf("EstimateTokensCalibrated(nil cal) = %d, want 41", got)
	}
}

// ── Bug B: estimateMessagesTokens counts reasoning + tool_use overhead ──

// TestIssue535_EstimateMessagesTokensCountsReasoningAndToolUse uses the
// issue's worked example: a message with 10K chars of reasoning content and
// 10 tool_use blocks. Before the fix estimateMessagesTokens returned ~98
// (30x low vs estimateTokens ~2981); both must now agree.
func TestIssue535_EstimateMessagesTokensCountsReasoningAndToolUse(t *testing.T) {
	reasoning := strings.Repeat("r", 10000) // 10K chars of reasoning
	content := []provider.ContentBlock{{Type: "text", Text: reasoning}}
	for i := 0; i < 10; i++ {
		content = append(content, provider.ContentBlock{
			Type:     "tool_use",
			ToolID:   "call",
			ToolName: "read_file",
			Input:    []byte(`{"path":"/a.go"}`), // 16 chars
		})
	}
	msg := provider.Message{Role: "assistant", Content: content}

	sliceEstimate := estimateMessagesTokens([]provider.Message{msg})
	perMsgEstimate := estimateTokensStandalone(msg)

	if sliceEstimate != perMsgEstimate {
		t.Errorf("estimateMessagesTokens (%d) != estimateTokens (%d) — accounting paths diverged (Bug B)",
			sliceEstimate, perMsgEstimate)
	}
	// Reasoning alone is 10000/3.5 ≈ 2858 tokens; the estimate must
	// reflect it (old value was ~98).
	if sliceEstimate < 2858 {
		t.Errorf("estimateMessagesTokens = %d, want ≥ 2858 (10K reasoning chars must be counted)", sliceEstimate)
	}
	// tool_use ×6 overhead must be included: without it the estimate would
	// be 60 tokens lower.
	if sliceEstimate < 2920 {
		t.Errorf("estimateMessagesTokens = %d, tool_use ×6 overhead appears missing", sliceEstimate)
	}
}

func TestIssue535_EstimateMessagesTokensReasoningSensitivity(t *testing.T) {
	// Direct before/after style check: adding reasoning content must raise
	// the estimate (it was invisible to the old implementation).
	plain := []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "answer"},
	}}}
	withReasoning := []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{
		{Type: "text", Text: "answer"},
		{Type: "text", Text: "", ReasoningContent: strings.Repeat("think ", 1000)}, // 6000 chars
	}}}
	a, b := estimateMessagesTokens(plain), estimateMessagesTokens(withReasoning)
	if b <= a {
		t.Errorf("reasoning content invisible to estimateMessagesTokens: plain=%d withReasoning=%d", a, b)
	}
	if b-a < 1700 { // 6000/3.5 ≈ 1714
		t.Errorf("reasoning undercounted: delta=%d, want ≥1700 (6000 chars @3.5)", b-a)
	}
}
