package agent

import (
	"github.com/topcheer/ggcode/internal/provider"
)

type thinkingAccumulator struct {
	blocks []provider.ContentBlock
	cur    int
	sawAny bool
}

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

func (a *thinkingAccumulator) onText(s string) {
	if s == "" {
		return
	}

	if a.cur < 0 || a.cur >= len(a.blocks) {
		a.blocks = append(a.blocks, provider.ContentBlock{Type: "text"})
		a.cur = len(a.blocks) - 1
	}
	a.blocks[a.cur].ReasoningContent += s
	a.sawAny = true
}

func (a *thinkingAccumulator) accumulated() []provider.ContentBlock {
	if !a.sawAny {
		return nil
	}
	return a.blocks
}

func (a *thinkingAccumulator) hasContent() bool {
	return a.sawAny
}
