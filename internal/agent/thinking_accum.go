package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// thinkingAccumulator collects streamed reasoning events into per-block
// entries, preserving interleaved-thinking structure (#228).
//
// Anthropic emits one signature per thinking block at content_block_start,
// BEFORE that block's text deltas. So each signature event opens a new
// block, and subsequent text deltas belong to it until the next signature
// opens another block. A scalar "last signature wins" accumulator paired N
// blocks into one with mismatched (content, signature) — the next request
// failed signature verification with a 400.
//
// Unsigned reasoning (DeepSeek-style: text only, no signature events)
// accumulates into a single "text"-typed block.
type thinkingAccumulator struct {
	blocks []provider.ContentBlock
	cur    int // index of the open block; out of range (incl. 0 zero value on empty slice) = none
	sawAny bool
}

// onSignature records a thinking-block signature, opening a new block.
func (a *thinkingAccumulator) onSignature(sig string) {
	if sig == "" {
		return
	}
	a.blocks = append(a.blocks, provider.ContentBlock{
		Type:              "thinking",
		ThinkingSignature: sig,
	})
	a.cur = len(a.blocks) - 1
	a.sawAny = true
}

// onText appends reasoning text to the open block, opening an unsigned
// block first if none is open (non-Anthropic providers).
func (a *thinkingAccumulator) onText(s string) {
	if s == "" {
		return
	}
	// Zero value cur=0 must read as "none open": range-check instead of a
	// -1 sentinel so `var a thinkingAccumulator` works directly.
	if a.cur < 0 || a.cur >= len(a.blocks) {
		a.blocks = append(a.blocks, provider.ContentBlock{Type: "text"})
		a.cur = len(a.blocks) - 1
	}
	a.blocks[a.cur].ReasoningContent += s
	a.sawAny = true
}

// blocks_ returns the accumulated blocks in stream order. Callers prepend
// them before tool_use blocks when echoing back.
func (a *thinkingAccumulator) accumulated() []provider.ContentBlock {
	if !a.sawAny {
		return nil
	}
	return a.blocks
}

// hasContent reports whether any reasoning text or signature was seen.
func (a *thinkingAccumulator) hasContent() bool {
	return a.sawAny
}

// plainText returns all reasoning text concatenated (for callers that need
// a single blob, e.g. display).
func (a *thinkingAccumulator) plainText() string {
	var b strings.Builder
	for i := range a.blocks {
		b.WriteString(a.blocks[i].ReasoningContent)
	}
	return b.String()
}
