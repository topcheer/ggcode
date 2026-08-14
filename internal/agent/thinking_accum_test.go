package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestThinkingAccumulator_MultiBlockSignature verifies interleaved-thinking
// streams preserve every (content, signature) pair as separate blocks
// instead of collapsing to the last signature with merged text (#228).
func TestThinkingAccumulator_MultiBlockSignature(t *testing.T) {
	var a thinkingAccumulator
	// Simulate: block A start (sig) → A text → tool_use (not reasoning) →
	// block B start (sig) → B text.
	a.onSignature("sig-A")
	a.onText("thought A part 1")
	a.onText("thought A part 2")
	a.onSignature("sig-B")
	a.onText("thought B")

	blocks := a.accumulated()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 thinking blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].ThinkingSignature != "sig-A" || blocks[0].ReasoningContent != "thought A part 1thought A part 2" {
		t.Errorf("block A mismatch: sig=%q content=%q", blocks[0].ThinkingSignature, blocks[0].ReasoningContent)
	}
	if blocks[1].ThinkingSignature != "sig-B" || blocks[1].ReasoningContent != "thought B" {
		t.Errorf("block B mismatch: sig=%q content=%q", blocks[1].ThinkingSignature, blocks[1].ReasoningContent)
	}
	if blocks[0].Type != "thinking" || blocks[1].Type != "thinking" {
		t.Errorf("signed blocks must be type thinking, got %q/%q", blocks[0].Type, blocks[1].Type)
	}
}

// TestThinkingAccumulator_UnsignedText verifies DeepSeek-style reasoning
// (text with no signature events) accumulates into a single text block.
func TestThinkingAccumulator_UnsignedText(t *testing.T) {
	var a thinkingAccumulator
	a.onText("step 1")
	a.onText("step 2")
	blocks := a.accumulated()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 unsigned block, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].ThinkingSignature != "" {
		t.Errorf("unsigned block should be text-type with empty sig: %+v", blocks[0])
	}
	if blocks[0].ReasoningContent != "step 1step 2" {
		t.Errorf("content mismatch: %q", blocks[0].ReasoningContent)
	}
}

// TestThinkingAccumulator_Empty verifies nothing is produced when no
// reasoning events arrive.
func TestThinkingAccumulator_Empty(t *testing.T) {
	var a thinkingAccumulator
	if a.hasContent() {
		t.Error("fresh accumulator should report no content")
	}
	if blocks := a.accumulated(); len(blocks) != 0 {
		t.Errorf("fresh accumulator should yield no blocks, got %v", blocks)
	}
}

// TestThinkingAccumulator_SignatureOnlyTextBeforeNextSig verifies text
// arriving after a signature belongs to the newly opened block even when
// interleaved with more signatures.
func TestThinkingAccumulator_SignatureOnlyTextBeforeNextSig(t *testing.T) {
	var a thinkingAccumulator
	a.onSignature("sig-1")
	a.onSignature("sig-2") // block 1 gets no text — still valid to echo back
	a.onText("only for 2")
	blocks := a.accumulated()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].ReasoningContent != "" {
		t.Errorf("block 1 should be empty, got %q", blocks[0].ReasoningContent)
	}
	if blocks[1].ReasoningContent != "only for 2" {
		t.Errorf("block 2 content mismatch: %q", blocks[1].ReasoningContent)
	}
}

// compile-time check that accumulated blocks are directly usable as
// provider message content.
var _ = []provider.ContentBlock((&thinkingAccumulator{}).accumulated())
