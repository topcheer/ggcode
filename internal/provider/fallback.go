package provider

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/debug"
)

// FailoverTrigger is the reason a failover was activated.
type FailoverTrigger string

const (
	FailoverTriggerQuota    FailoverTrigger = "quota_exhausted"
	FailoverTriggerAuth     FailoverTrigger = "auth_error"
	FailoverTriggerRepeated FailoverTrigger = "repeated_failures"
)

// failoverThreshold is the number of consecutive transient failures on the
// primary provider before switching to the fallback. We allow a few retries
// because transient hiccups are common, but sustained failure means the
// primary is effectively down.
const failoverThreshold = 3

// FallbackProvider wraps a primary provider and transparently fails over to
// a fallback provider when the primary experiences permanent errors (quota
// exhaustion, auth failure) or sustained transient failures.
//
// Design:
//   - On permanent failures (quota/auth), failover is immediate and sticky —
//     the primary won't recover without user intervention.
//   - On transient failures (rate limit, 5xx, network), the primary is retried
//     up to failoverThreshold consecutive times before switching.
//   - Once failed over, the fallback becomes the active provider for the
//     remainder of the session (sticky failover). The user can manually
//     switch back via /model or /vendor.
//   - A StreamEventSystem notification is emitted to inform the user of the
//     failover, so they understand why output may look different.
type FallbackProvider struct {
	primary     Provider
	fallback    Provider
	description string // human-readable label, e.g. "zai/glm-4.6 → anthropic/claude-sonnet"

	mu              sync.RWMutex
	failedOver      atomic.Bool
	consecutiveFail atomic.Int32

	// notify is called when a failover occurs, so the UI can inform the user.
	// It receives the trigger reason and the error that caused it.
	notify func(trigger FailoverTrigger, err error)

	// activeProvider returns the provider that should be used for the next call.
	// Under the lock, this is either primary or fallback.
}

// NewFallbackProvider creates a provider that tries primary first and falls
// back to the secondary on permanent or sustained failures.
func NewFallbackProvider(primary, fallback Provider, description string) *FallbackProvider {
	if description == "" {
		description = "primary → fallback"
	}
	return &FallbackProvider{
		primary:     primary,
		fallback:    fallback,
		description: description,
	}
}

// SetFailoverNotify sets a callback invoked when failover is activated.
func (f *FallbackProvider) SetFailoverNotify(fn func(trigger FailoverTrigger, err error)) {
	f.mu.Lock()
	f.notify = fn
	f.mu.Unlock()
}

// HasFailedOver reports whether the provider has switched to the fallback.
func (f *FallbackProvider) HasFailedOver() bool {
	return f.failedOver.Load()
}

// Reset clears the failover state, returning to the primary provider.
// Called when the user manually switches models/providers.
func (f *FallbackProvider) Reset() {
	f.failedOver.Store(false)
	f.consecutiveFail.Store(0)
}

// activeLocked returns the currently active provider (must hold f.mu).
func (f *FallbackProvider) activeLocked() Provider {
	if f.failedOver.Load() {
		return f.fallback
	}
	return f.primary
}

// shouldFailover determines whether the given error should trigger an
// immediate or eventual failover.
//
// Returns (triggerImmediately, trigger).
func shouldFailover(err error) (bool, FailoverTrigger) {
	if err == nil {
		return false, ""
	}
	switch ClassifyLLMError(err) {
	case FailureQuota:
		return true, FailoverTriggerQuota
	case FailureAuth:
		return true, FailoverTriggerAuth
	default:
		return false, ""
	}
}

// maybeFailover checks the error and decides whether to activate failover.
// Returns the error to return to the caller (nil if we should retry via fallback)
// and true if a retry on the fallback is possible.
//
// Must NOT be called under f.mu — this function acquires the write lock.
func (f *FallbackProvider) maybeFailover(err error) (error, bool) {
	if err == nil {
		// Success — reset consecutive failure counter.
		f.consecutiveFail.Store(0)
		return nil, false
	}

	immediate, trigger := shouldFailover(err)

	if !immediate {
		// Transient error — increment counter and check threshold.
		count := f.consecutiveFail.Add(1)
		if count < int32(failoverThreshold) {
			return err, false
		}
		trigger = FailoverTriggerRepeated
	}

	// Activate failover.
	f.mu.Lock()
	alreadyFailed := f.failedOver.Load()
	if !alreadyFailed {
		f.failedOver.Store(true)
		debug.Log("provider", "failover activated: %s (trigger=%s, error=%v)", f.description, trigger, err)
		if f.notify != nil {
			// Copy callback under lock to avoid race with SetFailoverNotify.
			notify := f.notify
			f.mu.Unlock()
			notify(trigger, err)
		} else {
			f.mu.Unlock()
		}
		return err, true
	}
	f.mu.Unlock()

	// Already failed over — no further fallback available.
	return err, false
}

// Name returns the name of the currently active provider.
func (f *FallbackProvider) Name() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.activeLocked().Name()
}

// Chat sends a non-streaming request, failing over on permanent errors.
func (f *FallbackProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()

	resp, err := active.Chat(ctx, messages, tools)
	if err == nil {
		f.consecutiveFail.Store(0)
		return resp, nil
	}

	// Check if we should failover.
	_, canRetry := f.maybeFailover(err)
	if !canRetry {
		return nil, err
	}

	// Retry on the fallback.
	f.mu.RLock()
	fallback := f.fallback
	f.mu.RUnlock()

	debug.Log("provider", "Chat failover retry on %s", fallback.Name())
	resp2, err2 := fallback.Chat(ctx, messages, tools)
	if err2 == nil {
		f.consecutiveFail.Store(0)
	}
	return resp2, err2
}

// ChatStream sends a streaming request, failing over on permanent errors
// that occur before streaming begins (connection/initialization errors).
// Mid-stream errors are NOT retried — partial output has already been sent.
func (f *FallbackProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()

	stream, err := active.ChatStream(ctx, messages, tools)
	if err == nil {
		f.consecutiveFail.Store(0)
		return stream, nil
	}

	// Check if we should failover.
	_, canRetry := f.maybeFailover(err)
	if !canRetry {
		return nil, err
	}

	// Retry on the fallback.
	f.mu.RLock()
	fallback := f.fallback
	f.mu.RUnlock()

	debug.Log("provider", "ChatStream failover retry on %s", fallback.Name())
	stream2, err2 := fallback.ChatStream(ctx, messages, tools)
	if err2 == nil {
		f.consecutiveFail.Store(0)
	}
	return stream2, err2
}

// CountTokens delegates to the active provider.
func (f *FallbackProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	return active.CountTokens(ctx, messages)
}

// --- Interface delegation ---

// ModelName returns the active provider's model name if it implements ModelNameProvider.
func (f *FallbackProvider) ModelName() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if mp, ok := active.(ModelNameProvider); ok {
		return mp.ModelName()
	}
	return ""
}

// SetReasoningEffort sets the effort on both providers if they support it.
func (f *FallbackProvider) SetReasoningEffort(effort string) {
	f.mu.RLock()
	primary := f.primary
	fallback := f.fallback
	f.mu.RUnlock()

	if rp, ok := primary.(ReasoningEffortProvider); ok {
		rp.SetReasoningEffort(effort)
	}
	if rp, ok := fallback.(ReasoningEffortProvider); ok {
		rp.SetReasoningEffort(effort)
	}
}

// ReasoningEffort returns the active provider's effort level.
func (f *FallbackProvider) ReasoningEffort() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if rp, ok := active.(ReasoningEffortProvider); ok {
		return rp.ReasoningEffort()
	}
	return ""
}

// SetSessionID sets the session ID on both providers if they support it.
func (f *FallbackProvider) SetSessionID(sessionID string) {
	f.mu.RLock()
	primary := f.primary
	fallback := f.fallback
	f.mu.RUnlock()

	if ss, ok := primary.(SessionIDSetter); ok {
		ss.SetSessionID(sessionID)
	}
	if ss, ok := fallback.(SessionIDSetter); ok {
		ss.SetSessionID(sessionID)
	}
}

// Description returns a human-readable label for this fallback configuration.
func (f *FallbackProvider) Description() string {
	return f.description
}

// String returns a debug-friendly representation.
func (f *FallbackProvider) String() string {
	active := "primary"
	if f.failedOver.Load() {
		active = "fallback"
	}
	return fmt.Sprintf("FallbackProvider(%s, active=%s)", f.description, active)
}
