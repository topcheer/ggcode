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

// isPromptTooLongError detects context-length errors from any provider.
func isPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	keywords := []string{
		"prompt too long",
		"context length",
		"context window",
		"maximum context",
		"too many tokens",
		"input is too long",
		"exceeds the model's context",
		"maximum input tokens",
	}
	for _, keyword := range keywords {
		if strings.Contains(s, keyword) {
			return true
		}
	}
	return false
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

	debug.Log("agent", "reactive compact: compacting conversation")
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

// maybeAutoCompact triggers auto-compaction if token usage exceeds the threshold.
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
	changed, err := a.contextManager.CheckAndSummarize(ctx, a.provider)
	if err != nil {
		if shouldIgnoreAutoCompactError(err) {
			debug.Log("agent", "ignoring transient auto-compact failure: %v", err)
			if transientWarned == nil || !*transientWarned {
				debug.Log("agent", "transient compact error details: %s", compactErrorReason(err))
				if transientWarned != nil {
					*transientWarned = true
				}
			}
			return nil
		}
		return fmt.Errorf("auto-summarize failed: %w", err)
	}
	if transientWarned != nil {
		*transientWarned = false
	}
	if !changed {
		return nil
	}

	newTokens := a.contextManager.TokenCount()
	debug.Log("agent", "auto-compact: conversation compacted successfully (%d → %d tokens)", tokens, newTokens)

	// If token reduction is significant (>30%), likely a summarize happened.
	// Persist checkpoint so --resume won't need to re-compact.
	if newTokens < tokens*7/10 {
		a.maybeSaveCheckpoint()
	}

	return nil
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
