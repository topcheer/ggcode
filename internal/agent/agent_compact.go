package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const maxReactiveCompactRetries = 3

// fallbackCheckpointThreshold is the message count at which we force a
// checkpoint even if compaction never succeeded. This prevents unbounded
// context growth when the summarization LLM call keeps failing (e.g., model
// doesn't support it, rate limits, network issues). The agent keeps running
// in autopilot mode, accumulating messages without any checkpoint, which
// leads to multi-million-token sessions that take minutes to restore.
const fallbackCheckpointThreshold = 500

// maybeFallbackCheckpoint saves a checkpoint when the message count exceeds
// fallbackCheckpointThreshold, even if no compaction occurred. This is a
// safety net for sessions where compaction keeps failing.
func (a *Agent) maybeFallbackCheckpoint() {
	a.mu.RLock()
	fn := a.onCheckpoint
	a.mu.RUnlock()
	if fn == nil {
		return
	}
	msgs := a.contextManager.Messages()
	if len(msgs) < fallbackCheckpointThreshold {
		return
	}
	// Only save if we haven't saved a checkpoint recently (avoid spamming).
	if a.lastCheckpointMessageCount == len(msgs) {
		return
	}
	a.lastCheckpointMessageCount = len(msgs)
	tokenCount := a.contextManager.TokenCount()
	debug.Log("agent", "fallback checkpoint: %d messages, %d tokens (compaction may have failed)", len(msgs), tokenCount)
	fn(msgs, tokenCount)
}

// MicrocompactIfOverThreshold is kept as a no-op for API compatibility.
// Microcompact was removed — precompact at 97.5% + reactive compact on PTL
// now handle all compaction needs without corrupting tool_result data.
func (a *Agent) MicrocompactIfOverThreshold() (compacted bool, beforeTokens int, afterTokens int) {
	tokens := a.contextManager.TokenCount()
	return false, tokens, tokens
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
	debug.Log("agent", "tryReactiveCompact: PTL detected, tokens=%d maxTokens=%d attempting compact", tokens, a.contextManager.ContextWindow())

	// Infer actual context window from the overflow error.
	a.mu.Lock()
	pk := a.probeKey
	a.mu.Unlock()
	if window := provider.InferContextWindowFromError(
		err,
		tokens,
		a.contextManager.ContextWindow(),
		pk,
		func(n int) { a.contextManager.SetContextWindow(n) },
	); window > 0 {
		debug.Log("agent", "inferred context window from overflow error: %d", window)
	}

	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Context overflow detected, compressing...] "})

	if a.consumeReadyPreCompact(nil) {
		debug.Log("agent", "reactive compact: consumed completed precompact")
		onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Context compressed via pre-compact] "})
		if retries != nil {
			*retries = *retries + 1
		}
		return true
	}
	// Cancel any still-running precompact before modifying live messages.
	a.CancelPreCompact()
	if a.compactLocallyForSendBudget("reactive compact") {
		if retries != nil {
			*retries = *retries + 1
		}
		return true
	}

	debug.Log("agent", "reactive compact: compacting conversation")
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Compressing conversation via summarization...] "})
	changed := false
	changed, compactErr := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if compactErr != nil {
		debug.Log("agent", "reactive compact: summarization failed (%v), falling back to truncation", compactErr)
	}

	// Always try truncation as a fallback, even if summarization failed.
	// This prevents the context from growing unbounded when the summarization
	// LLM call keeps failing (e.g., model doesn't support it, rate limits).
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
	// Always save checkpoint after reactive compact — the live context was
	// modified and must be persisted so --resume can restore the compacted
	// state instead of re-loading the full pre-compaction message history.
	a.maybeSaveCheckpoint()
	if retries != nil {
		*retries = *retries + 1
		debug.Log("agent", "reactive compact retry=%d", *retries)
	}
	return true
}

// maybeAutoCompact keeps the hot LLM path non-blocking. When token usage
// exceeds 97.5% of the context window, it schedules a background precompact
// (LLM summarization). If precompact succeeds, the next turn will consume
// the result and shrink the context. If precompact fails or is too slow,
// reactive compact (PTL recovery) handles it synchronously.
func (a *Agent) maybeAutoCompact(ctx context.Context, onEvent func(provider.StreamEvent), transientWarned *bool) error {
	ratio := a.contextManager.UsageRatio()
	tokens := a.contextManager.TokenCount()

	debug.Log("agent", "maybeAutoCompact: tokens=%d ratio=%.3f maxTokens=%d",
		tokens, ratio, a.contextManager.ContextWindow())

	// Trigger precompact at 97.5% of context window
	if ratio < precompactTriggerRatio {
		return nil
	}

	// If a precompact is already running, skip entirely
	a.mu.Lock()
	precompactRunning := a.precompact != nil
	cooldownUntil := a.precompactCooldownUntil
	a.mu.Unlock()
	if precompactRunning {
		debug.Log("agent", "maybeAutoCompact: SKIP (precompact already running, tokens=%d)", tokens)
		return nil
	}

	// Cooldown: after a precompact attempt (success or failure), wait before
	// trying again.
	if time.Now().Before(cooldownUntil) {
		debug.Log("agent", "maybeAutoCompact: SKIP (cooldown active, %s remaining, tokens=%d)", time.Until(cooldownUntil).Round(time.Second), tokens)
		return nil
	}

	// Schedule background precompact
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context (%d tokens)...] ", tokens)})
	const precompactCooldown = 2 * time.Minute
	a.mu.Lock()
	a.precompactCooldownUntil = time.Now().Add(precompactCooldown)
	a.mu.Unlock()

	debug.Log("agent", "maybeAutoCompact: scheduling background precompact tokens=%d ratio=%.3f cooldown=%s", tokens, ratio, precompactCooldown)
	a.StartPreCompact()
	return nil
}

const precompactTriggerRatio = 0.975

func (a *Agent) ensurePromptSendable() {
	if a.promptBudget() <= 0 {
		return
	}
	if a.contextManager.TokenCount() < a.promptBudget() {
		return
	}
	// Consume completed precompact first
	if a.consumeReadyPreCompact(nil) && a.contextManager.TokenCount() < a.promptBudget() {
		return
	}

	// If precompact is still running, we can't safely modify live messages.
	// The prompt will likely exceed budget and trigger reactive compact (PTL).
	a.mu.RLock()
	precompactRunning := a.precompact != nil
	a.mu.RUnlock()
	if precompactRunning {
		debug.Log("agent", "ensurePromptSendable: SKIP (precompact running, will PTL if needed)")
		return
	}

	// Last-resort: truncate oldest message groups to fit the budget.
	// This is destructive but better than failing to send.
	a.compactLocallyForSendBudget("pre-send hard guard")
}

func (a *Agent) promptBudget() int {
	if cm, ok := a.contextManager.(promptBudgeter); ok {
		return cm.PromptBudget()
	}
	return a.contextManager.ContextWindow()
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

	// Only truncation remains — no microcompact (removed to preserve
	// tool_result integrity for precompact summarization).
	changed := false
	tokens := before
	dropped := 0
	if cm, ok := a.contextManager.(oldestGroupTruncater); ok {
		for tokens >= budget && cm.TruncateOldestGroupForRetry() {
			changed = true
			dropped++
			tokens = a.contextManager.TokenCount()
		}
	}

	if changed {
		debug.Log("agent", "%s: truncation reduced context %d→%d tokens budget=%d dropped_groups=%d",
			reason, before, tokens, budget, dropped)
		a.maybeSaveCheckpoint()
	} else {
		debug.Log("agent", "%s: truncation unavailable/ineffective tokens=%d budget=%d", reason, before, budget)
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

// SaveCheckpoint persists the current context as a checkpoint.  Called
// externally (e.g. by slash /compact) after synchronous compaction.
func (a *Agent) SaveCheckpoint() {
	a.maybeSaveCheckpoint()
}
