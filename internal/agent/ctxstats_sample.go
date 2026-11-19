package agent

import (
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/ctxstats"
	"github.com/topcheer/ggcode/internal/provider"
)

// samplePromptCacheStats appends one ctxstats JSONL line for this LLM turn
// (#5812) - the memstats-mirror for prompt-cache health. Called at the
// provider-call boundary right after usage lands, with the exact tool
// definitions that were sent; no-op unless GGCODE_CTX_SAMPLE or the
// ~/.ggcode/ctx-sample marker enables sampling.
func (a *Agent) samplePromptCacheStats(usage provider.TokenUsage, tools []provider.ToolDefinition) {
	if !ctxstats.Enabled() {
		return
	}
	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		return
	}
	msgs := cm.Messages()
	var head string
	tailBytes := 0
	for i, msg := range msgs {
		isHead := i == 0 && msg.Role == "system"
		for _, b := range msg.Content {
			if isHead && b.Type == "text" {
				head += b.Text
				continue
			}
			tailBytes += len(b.Text) + len(b.Output) + len(b.ReasoningContent)
		}
	}
	providerName := ""
	if p := a.Provider(); p != nil {
		providerName = p.Name()
	}
	ctxstats.SampleTurn(ctxstats.TurnInput{
		Provider:      providerName,
		SystemHead:    head,
		Tools:         tools,
		TailBytes:     tailBytes,
		NonTailMutSeq: cm.NonTailMutSeq(),
		MutSources:    cm.DrainMutationSources(),
		Usage:         usage,
	})
}
