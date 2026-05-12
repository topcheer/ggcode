package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const maxReactiveCompactRetries = 3

type microcompacter interface {
	Microcompact() bool
}

type promptBudgeter interface {
	PromptBudget() int
}

type oldestGroupTruncater interface {
	TruncateOldestGroupForRetry() bool
}

// isPromptTooLongError detects context-length errors from any provider.
func isPromptTooLongError(err error) bool {
	return provider.IsContextOverflowError(err)
}

// tryReactiveCompact attempts compaction after a prompt-too-long error.
// Returns true if compaction succeeded and the caller should retry.
func (a *Agent) tryReactiveCompact(ctx context.Context, onEvent func(provider.StreamEvent), err error, retries *int) bool {
	if !isPromptTooLongError(err) {
		debug.Log("agent", "tryReactiveCompact: not a PTL error: %v", err)
		return false
	}
	if retries != nil && *retries >= maxReactiveCompactRetries {
		debug.Log("agent", "tryReactiveCompact: max retries (%d) reached", *retries)
		return false
	}
	tokens := a.contextManager.TokenCount()
	debug.Log("agent", "tryReactiveCompact: PTL detected, tokens=%d attempting compact", tokens)
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Context overflow detected, compressing...] "})

	if a.consumeReadyPreCompact() {
		debug.Log("agent", "reactive compact: consumed completed precompact")
		onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Context compressed via pre-compact] "})
		if retries != nil {
			*retries = *retries + 1
		}
		return true
	}
	if a.compactLocallyForSendBudget("reactive compact") {
		if retries != nil {
			*retries = *retries + 1
		}
		return true
	}

	debug.Log("agent", "reactive compact: compacting conversation")
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Compressing conversation via summarization...] "})
	changed, compactErr := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if compactErr != nil {
		return false
	}

	if cm, ok := a.contextManager.(interface{ TruncateOldestGroupForRetry() bool }); ok {
		if cm.TruncateOldestGroupForRetry() {
			changed = true
		}
	}

	if !changed {
		return false
	}
	debug.Log("agent", "reactive compact: conversation compacted successfully")
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Context compressed (%d → %d tokens), retrying...] ", tokens, a.contextManager.TokenCount())})
	newTokens := a.contextManager.TokenCount()
	if newTokens < tokens*7/10 {
		a.maybeSaveCheckpoint()
	}
	if retries != nil {
		*retries = *retries + 1
		debug.Log("agent", "reactive compact retry=%d", *retries)
	}
	return true
}

// maybeAutoCompact keeps the hot LLM path non-blocking. It may perform local
// microcompaction, but full LLM summarization is scheduled through background
// pre-compaction and only adopted later when ready. Prompt-too-long recovery
// remains synchronous in tryReactiveCompact because the current request cannot
// proceed without shrinking context.
func (a *Agent) maybeAutoCompact(ctx context.Context, onEvent func(provider.StreamEvent), transientWarned *bool) error {
	threshold := a.contextManager.AutoCompactThreshold()
	tokens := a.contextManager.TokenCount()
	ratio := a.contextManager.UsageRatio()
	debug.Log("agent", "maybeAutoCompact: tokens=%d threshold=%d ratio=%.3f maxTokens=%d",
		tokens, threshold, ratio, a.contextManager.MaxTokens())
	if threshold <= 0 || tokens < threshold {
		return nil
	}

	debug.Log("agent", "maybeAutoCompact: TRIGGERED (tokens=%d >= threshold=%d)", tokens, threshold)
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context (%d tokens)...] ", tokens)})
	changed := false
	if cm, ok := a.contextManager.(microcompacter); ok {
		changed = cm.Microcompact()
	}
	newTokens := a.contextManager.TokenCount()
	if changed {
		debug.Log("agent", "auto-microcompact: conversation compacted locally (%d → %d tokens)", tokens, newTokens)
		if transientWarned != nil {
			*transientWarned = false
		}
		if newTokens < tokens*7/10 {
			a.maybeSaveCheckpoint()
		}
	}

	if newTokens < threshold {
		return nil
	}

	debug.Log("agent", "maybeAutoCompact: scheduling background precompact after microcompact tokens=%d threshold=%d", newTokens, threshold)
	a.StartPreCompact()
	return nil
}

func (a *Agent) ensurePromptSendable() {
	if a.promptBudget() <= 0 {
		return
	}
	if a.contextManager.TokenCount() < a.promptBudget() {
		return
	}
	if a.consumeReadyPreCompact() && a.contextManager.TokenCount() < a.promptBudget() {
		return
	}
	a.compactLocallyForSendBudget("pre-send hard guard")
}

func (a *Agent) promptBudget() int {
	if cm, ok := a.contextManager.(promptBudgeter); ok {
		return cm.PromptBudget()
	}
	return a.contextManager.MaxTokens()
}

func (a *Agent) compactLocallyForSendBudget(reason string) bool {
	budget := a.promptBudget()
	if budget <= 0 {
		return false
	}
	before := a.contextManager.TokenCount()
	if before < budget {
		return false
	}

	changed := false
	if cm, ok := a.contextManager.(microcompacter); ok && cm.Microcompact() {
		changed = true
	}

	tokens := a.contextManager.TokenCount()
	dropped := 0
	if tokens >= budget {
		if cm, ok := a.contextManager.(oldestGroupTruncater); ok {
			for tokens >= budget && cm.TruncateOldestGroupForRetry() {
				changed = true
				dropped++
				tokens = a.contextManager.TokenCount()
			}
		}
	}

	if changed {
		debug.Log("agent", "%s: local compaction reduced context %d→%d tokens budget=%d dropped_groups=%d",
			reason, before, tokens, budget, dropped)
		a.maybeSaveCheckpoint()
	} else {
		debug.Log("agent", "%s: local compaction unavailable/ineffective tokens=%d budget=%d", reason, before, budget)
	}
	return changed
}

// shouldIgnoreAutoCompactError returns true for transient network/timeout errors
// that should not abort the agent loop.
func shouldIgnoreAutoCompactError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isPromptTooLongError(err) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, keyword := range []string{
		"unexpected eof",
		"connection reset by peer",
		"broken pipe",
		"timeout awaiting response headers",
		"tls handshake timeout",
		"server closed idle connection",
		"temporary failure in name resolution",
	} {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
}

// compactErrorReason returns a human-readable summary of a compaction error.
func compactErrorReason(err error) string {
	if err == nil {
		return "unknown error"
	}
	text := strings.TrimSpace(err.Error())
	text = strings.TrimPrefix(text, "summarization call failed: ")
	text = strings.TrimPrefix(text, "auto-summarize failed: ")
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 120 {
		text = text[:117] + "..."
	}
	return text
}

// forceCompactAndPause compacts the conversation unconditionally, used by the
// autopilot loop guard to break out of repetitive continuation loops.
func (a *Agent) forceCompactAndPause(ctx context.Context, onEvent func(provider.StreamEvent)) error {
	debug.Log("agent", "autopilot loop guard triggered; compacting and pausing")
	tokens := a.contextManager.TokenCount()

	compacted, err := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if err != nil {
		return err
	}
	if !compacted {
		if err := a.contextManager.Summarize(ctx, a.provider); err != nil {
			return err
		}
		compacted = true
	}
	newTokens := a.contextManager.TokenCount()
	debug.Log("agent", "autopilot loop guard: compact completed (%d → %d tokens)", tokens, newTokens)
	// Always save checkpoint for forced compaction — it's initiator-driven
	// and represents a deliberate state transition.
	a.maybeSaveCheckpoint()
	return nil
}

// maybeSaveCheckpoint triggers the checkpoint callback if one is registered.
// This persists the compacted message state so --resume can skip re-compacting.
func (a *Agent) maybeSaveCheckpoint() {
	a.mu.RLock()
	fn := a.onCheckpoint
	a.mu.RUnlock()

	if fn == nil {
		return
	}

	msgs, tokenCount := a.contextManager.MessagesAndTokenCount()
	fn(msgs, tokenCount)
}
