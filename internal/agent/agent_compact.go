package agent

import (
	"context"
	"fmt"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
)

const maxReactiveCompactRetries = 3

const fallbackCheckpointThreshold = 500

const fallbackCheckpointInterval = 50

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

	a.mu.Lock()
	if len(msgs)-a.lastCheckpointMessageCount < fallbackCheckpointInterval {
		a.mu.Unlock()
		return
	}
	a.lastCheckpointMessageCount = len(msgs)
	a.mu.Unlock()
	tokenCount := a.contextManager.TokenCount()
	debug.Log("agent", "fallback checkpoint: %d messages, %d tokens (compaction may have failed)", len(msgs), tokenCount)

	fallbackID := a.contextManager.SummaryMsgID()
	if fallbackID == "" {

		debug.Log("checkpoint", "fallback checkpoint: no summary message; skipping (tail anchor would replace history with one message)")
		return
	}

	lmid := ""
	if n := len(msgs); n > 0 {
		lmid = msgs[n-1].ID
	}
	fn(fallbackID, lmid, tokenCount)
}

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

func isPromptTooLongError(err error) bool {
	return provider.IsContextOverflowError(err)
}

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
		a.resetGuidanceCounters()
		onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Context compressed via pre-compact] "})
		if retries != nil {
			*retries = *retries + 1
		}
		return true
	}

	a.CancelPreCompact()
	if a.compactLocallyForSendBudget("reactive compact") {
		if retries != nil {
			*retries = *retries + 1
		}
		return true
	}

	debug.Log("agent", "reactive compact: compacting conversation")
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "[Compressing conversation via summarization...] "})

	if res := hooks.RunPreCompactHooks(a.GetHookConfig(), hooks.HookEnv{
		TokenBefore:    tokens,
		CompactTrigger: "reactive",
	}); res.Err != nil {
		debug.Log("hooks", "pre_compact hook error (non-fatal): %v", res.Err)
	}

	a.preflushFacts("reactive", a.contextManager.Messages())
	var lastMsgID string
	if msgs := a.contextManager.Messages(); len(msgs) > 0 {
		lastMsgID = msgs[len(msgs)-1].ID
	}
	changed := false
	changed, compactErr := a.contextManager.CheckAndSummarize(ctx, a.auxProviderFor())
	if compactErr != nil {
		debug.Log("agent", "reactive compact: summarization failed (%v), falling back to truncation", compactErr)
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
	a.resetGuidanceCounters()
	newTokens := a.contextManager.TokenCount()
	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Context compressed (%d → %d tokens), retrying...] ", tokens, newTokens)})

	hookCfg := a.GetHookConfig()
	hooks.RunCompactionHooks(hookCfg, hooks.HookEnv{
		TokenBefore:    tokens,
		TokenAfter:     newTokens,
		CompactTrigger: "reactive",
	})

	a.maybeSaveCheckpoint(lastMsgID)
	if retries != nil {
		*retries = *retries + 1
		debug.Log("agent", "reactive compact retry=%d", *retries)
	}
	return true
}

func (a *Agent) maybeAutoCompact(ctx context.Context, onEvent func(provider.StreamEvent), transientWarned *bool) error {
	tokens := a.contextManager.TokenCount()
	threshold := a.contextManager.AutoCompactThreshold()

	debug.Log("agent", "maybeAutoCompact: tokens=%d threshold=%d maxTokens=%d",
		tokens, threshold, a.contextManager.ContextWindow())

	if tokens < threshold {
		return nil
	}

	a.mu.Lock()
	precompactRunning := a.precompact != nil
	cooldownUntil := a.precompactCooldownUntil
	a.mu.Unlock()
	if precompactRunning {
		debug.Log("agent", "maybeAutoCompact: SKIP (precompact already running, tokens=%d)", tokens)
		return nil
	}

	if time.Now().Before(cooldownUntil) {
		debug.Log("agent", "maybeAutoCompact: SKIP (cooldown active, %s remaining, tokens=%d)", time.Until(cooldownUntil).Round(time.Second), tokens)
		return nil
	}

	onEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: fmt.Sprintf("[Auto-compressing context (%d tokens)...] ", tokens)})
	const precompactCooldown = 2 * time.Minute
	a.mu.Lock()
	a.precompactCooldownUntil = time.Now().Add(precompactCooldown)
	a.mu.Unlock()

	debug.Log("agent", "maybeAutoCompact: scheduling background precompact tokens=%d threshold=%d cooldown=%s", tokens, threshold, precompactCooldown)

	if msgs, _ := a.contextManager.MessagesAndTokenCount(); len(msgs) > 0 {
		bd := ctxpkg.AnalyzeBudget(msgs)
		debug.Log("agent", "context budget breakdown:\n%s", bd.FormatHumanReadable())
	}

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

	if a.consumeReadyPreCompact(nil) && a.contextManager.TokenCount() < a.promptBudget() {
		return
	}

	a.mu.RLock()
	precompactRunning := a.precompact != nil
	a.mu.RUnlock()
	if precompactRunning {
		debug.Log("agent", "ensurePromptSendable: SKIP (precompact running, will PTL if needed)")
		return
	}

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

func (a *Agent) maybeSaveCheckpoint(lastMsgID ...string) {
	a.mu.RLock()
	fn := a.onCheckpoint
	a.mu.RUnlock()

	if fn == nil {
		return
	}

	summaryMsgID := a.contextManager.SummaryMsgID()
	tokens := a.contextManager.TokenCount()
	if summaryMsgID == "" {
		debug.Log("checkpoint", "maybeSaveCheckpoint: no summary message found, skipping")
		return
	}

	lmid := ""
	if len(lastMsgID) > 0 {
		lmid = lastMsgID[0]
	}
	if lmid == "" {

		msgs := a.contextManager.Messages()
		if len(msgs) > 0 {
			lmid = msgs[len(msgs)-1].ID
		}
	}
	fn(summaryMsgID, lmid, tokens)

	a.mu.Lock()
	a.lastCheckpointMessageCount = len(a.contextManager.Messages())
	a.mu.Unlock()
}

func (a *Agent) SaveCheckpointWithLastMsgID(lastMsgID string) {
	a.maybeSaveCheckpoint(lastMsgID)
}
