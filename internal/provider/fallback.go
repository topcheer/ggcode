package provider

import (
	"context"
	"errors"
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
	// FailoverTriggerBack fires when the FALLBACK (active side) failed hard
	// and the wrapper switched back to the primary. Without this, a fallback
	// hitting its own quota/auth limit left the app permanently unusable —
	// every call errored with no recovery path short of manual /model.
	FailoverTriggerBack FailoverTrigger = "fallback_failed_back_to_primary"
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
// and true if a retry on the other provider is possible.
//
// Bidirectional (#936): when the FALLBACK is the active side and it fails
// hard (quota/auth/sustained transient), the wrapper switches BACK to the
// primary and grants a retry there. The sides ping-pong across calls — each
// individual call still bounds itself to one switch (active attempt + one
// retry on the other side), so a both-down outage costs two failed requests
// per call rather than an infinite in-call loop.
//
// Must NOT be called under f.mu — this function acquires the write lock.
func (f *FallbackProvider) maybeFailover(err error, failed Provider) (error, bool) {
	if err == nil {
		// Success — reset consecutive failure counter.
		f.consecutiveFail.Store(0)
		return nil, false
	}

	// #722: retry-budget exhaustion means the inner retry loop already gave
	// this provider a best-effort chance (the full 20-attempt backoff budget
	// sleeps ~7.5min; demanding failoverThreshold such calls blocks headless
	// callers ~23min). Treat it as sustained failure and switch NOW.
	// Checked before the FailureNone gate below because the sentinel wraps a
	// genuine provider failure that was already classified retryable.
	budgetExhausted := errors.Is(err, errRetryBudgetExhausted)

	// #304: user cancellation is not a provider failure. ClassifyLLMError
	// already returns FailureNone for context.Canceled ("non-model failure"),
	// but the counter below consumed every non-quota/auth error — 3
	// consecutive cancels on the non-streaming path would sticky-failover a
	// healthy primary for the rest of the session.
	if !budgetExhausted && ClassifyLLMError(err) == FailureNone {
		return err, false
	}
	// #303: context overflow is a request-size problem handled by agent-side
	// compaction, not provider health — don't count it toward failover either.
	if IsContextOverflowError(err) {
		return err, false
	}

	immediate, trigger := shouldFailover(err)
	if budgetExhausted {
		immediate, trigger = true, FailoverTriggerRepeated
	}

	if !immediate {
		// Transient error — increment counter and check threshold.
		count := f.consecutiveFail.Add(1)
		if count < int32(failoverThreshold) {
			return err, false
		}
		trigger = FailoverTriggerRepeated
	}

	// Activate failover (or switch back, #936).
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

	// Already failed over. If the caller's failed attempt was against the
	// PRIMARY (it read active before failover activated), it has never
	// touched the fallback and still deserves one retry (fix #164).
	if failed == f.primary {
		return err, true
	}
	// The failed call was on the FALLBACK and it failed hard. Switch back to
	// the primary (#936): a fallback that exhausted its own quota/auth must
	// not strand the session — the primary may have recovered since the
	// original failover (quotas reset, auth refreshed, transient outage
	// over). The caller retries on the primary; if it still fails, the next
	// call ping-pongs again — availability over stickiness.
	if failed == f.fallback {
		f.mu.Lock()
		f.failedOver.Store(false)
		debug.Log("provider", "failover back to primary: %s (trigger=%s, error=%v)", f.description, trigger, err)
		if f.notify != nil {
			notify := f.notify
			f.mu.Unlock()
			notify(FailoverTriggerBack, err)
		} else {
			f.mu.Unlock()
		}
		return err, true
	}
	return err, false
}

// Name returns the name of the currently active provider.
func (f *FallbackProvider) Name() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.activeLocked().Name()
}

// otherThan returns the provider that is NOT the given one (must hold no
// lock; both fields are immutable after construction).
func (f *FallbackProvider) otherThan(p Provider) Provider {
	if p == f.fallback {
		return f.primary
	}
	return f.fallback
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
	_, canRetry := f.maybeFailover(err, active)
	if !canRetry {
		return nil, err
	}

	// Retry on the other provider (fallback after a primary failure, or back
	// on the primary after the fallback failed — #936).
	other := f.otherThan(active)

	debug.Log("provider", "Chat failover retry on %s", other.Name())
	resp2, err2 := other.Chat(ctx, messages, tools)
	if err2 == nil {
		f.consecutiveFail.Store(0)
		return resp2, nil
	}
	// #454: preserve BOTH root causes. Returning only err2 let the
	// fallback's network error mask a primary quota/auth failure in
	// ClassifyLLMError (keyword-ordered: quota hits first when present),
	// skewing health reporting and retry decisions when both sides fail.
	return nil, errors.Join(err, err2)
}

// ChatStream sends a streaming request, failing over on permanent errors
// that occur before streaming begins (connection/initialization errors).
// Mid-stream errors are NOT retried — partial output has already been sent.
//
// All bundled providers return their channel immediately and report
// quota/auth/connection failures as ASYNC StreamEventError events, so the
// sync-error path alone never fires for them. The returned channel is
// therefore wrapped: the first Error event observed before any text arrives
// is routed through maybeFailover, and when it trips the failover the
// fallback's stream is transparently substituted (fresh request, no partial
// output lost). Errors after output has started still pass through
// unchanged (#371).
func (f *FallbackProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()

	stream, err := active.ChatStream(ctx, messages, tools)
	if err == nil {
		return f.watchStreamForFailover(ctx, active, stream, messages, tools), nil
	}

	// Check if we should failover.
	_, canRetry := f.maybeFailover(err, active)
	if !canRetry {
		return nil, err
	}

	// Retry on the other provider (#936: may be back on the primary when
	// the fallback was the side that failed).
	other := f.otherThan(active)

	debug.Log("provider", "ChatStream failover retry on %s", other.Name())
	stream2, err2 := other.ChatStream(ctx, messages, tools)
	if err2 == nil {
		f.consecutiveFail.Store(0)
		return stream2, nil
	}
	// #454: same root-cause preservation as Chat — join both errors so the
	// primary's quota/auth cause is not masked by the fallback's network error.
	return nil, errors.Join(err, err2)
}

// watchStreamForFailover relays a provider's stream while watching the first
// error-before-output event for failover-worthy failures. Once any
// text/output event has been relayed, errors pass through unchanged (partial
// output must not be retried). When failover activates before output, the
// fallback provider's stream replaces the remainder of this one.
func (f *FallbackProvider) watchStreamForFailover(ctx context.Context, failed Provider, stream <-chan StreamEvent, messages []Message, tools []ToolDefinition) <-chan StreamEvent {
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		sawOutput := false
		resetOnSuccess := func() {
			if sawOutput {
				// A stream that delivered output and ended cleanly proves the
				// provider works — reset the transient-failure streak, same
				// semantics the sync path's success return used to carry
				// (#376). Without this, two stale transient errors plus any
				// later error would stickily fail over a healthy primary.
				f.consecutiveFail.Store(0)
			}
		}
		// #602(R2): every channel operation below watches ctx. `out` is
		// unbuffered and consumers stop reading the moment they cancel;
		// before this, a cancelled turn parked this goroutine on `out <-`,
		// which parked the drain goroutine, which let the provider's own
		// buffered channel fill — three stuck layers leaked per cancelled
		// turn in long sessions/daemons.
		send := func(ev StreamEvent) bool {
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for {
			var ev StreamEvent
			select {
			case next, ok := <-stream:
				if !ok {
					resetOnSuccess()
					return
				}
				ev = next
			case <-ctx.Done():
				return
			}
			if !sawOutput && ev.Type == StreamEventError && ev.Error != nil {
				_, canRetry := f.maybeFailover(ev.Error, failed)
				// canRetry alone decides, matching the sync Chat/ChatStream
				// paths: maybeFailover returns true both when it JUST
				// activated failover and when the failed call hit the primary
				// after an earlier activation (#164 retry-once). The old
				// `&& !f.failedOver.Load()` gate made this branch dead code —
				// canRetry=true always co-occurs with failedOver=true (#390).
				if canRetry {
					other := f.otherThan(failed)
					debug.Log("provider", "ChatStream async-error failover on %s", other.Name())
					stream2, err2 := other.ChatStream(ctx, messages, tools)
					if err2 == nil {
						// Drain the failed stream and relay the fallback's.
						// #602(R2): the drain must be cancellable too — a
						// plain `for range stream` lived until the producer
						// closed, adding one parked goroutine per cancelled
						// turn.
						go func() {
							for {
								select {
								case _, ok := <-stream:
									if !ok {
										return
									}
								case <-ctx.Done():
									return
								}
							}
						}()
						if !send(StreamEvent{
							Type: StreamEventSystem,
							Text: fmt.Sprintf("active provider failed (%v); failing over to %s", ev.Error, other.Name()),
						}) {
							return
						}
						for {
							select {
							case ev2, ok := <-stream2:
								if !ok {
									resetOnSuccess()
									return
								}
								// #577(D): ANY content event (text, reasoning,
								// tool-call, done) proves the fallback delivered —
								// not just text. Counting text alone left pure
								// tool-call/reasoning successes from clearing
								// consecutiveFail, so stale counts prematurely
								// failed over a healthy primary (#376 semantics).
								// Mirrors the primary-stream rule at the bottom of
								// this loop.
								if ev2.Type != StreamEventError {
									sawOutput = sawOutput || ev2.Type != StreamEventSystem
								}
								if !send(ev2) {
									return
								}
							case <-ctx.Done():
								return
							}
						}
					}
					// Fallback stream could not even start — surface the
					// original error.
					if !send(ev) {
						return
					}
					continue
				}
			}
			sawOutput = sawOutput || (ev.Type != StreamEventError && ev.Type != StreamEventSystem)
			if !send(ev) {
				return
			}
		}
	}()
	return out
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

// --- Optional interface forwarding (#372) ---
//
// The agent (and subagent runner) probe the provider for these optional
// capabilities via type assertions. Without forwarding them, wrapping a
// provider in FallbackProvider silently disabled tool_choice, adaptive
// sampling, rate-limit warnings, and — most damaging — named-subagent model
// overrides (CloneWithModel fell back to "keep the original provider").

// SetToolChoice forwards to both wrapped providers (per-call state that must
// stay in sync regardless of which one is active).
func (f *FallbackProvider) SetToolChoice(choice string) {
	f.mu.RLock()
	primary := f.primary
	fallback := f.fallback
	f.mu.RUnlock()
	if tc, ok := primary.(ToolChoiceProvider); ok {
		tc.SetToolChoice(choice)
	}
	if tc, ok := fallback.(ToolChoiceProvider); ok {
		tc.SetToolChoice(choice)
	}
}

// ToolChoice returns the active provider's tool choice.
func (f *FallbackProvider) ToolChoice() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if tc, ok := active.(ToolChoiceProvider); ok {
		return tc.ToolChoice()
	}
	return ""
}

// SetTemperature forwards to both wrapped providers.
func (f *FallbackProvider) SetTemperature(temp float64) {
	f.mu.RLock()
	primary := f.primary
	fallback := f.fallback
	f.mu.RUnlock()
	if sc, ok := primary.(SamplingConfigProvider); ok {
		sc.SetTemperature(temp)
	}
	if sc, ok := fallback.(SamplingConfigProvider); ok {
		sc.SetTemperature(temp)
	}
}

// Temperature returns the active provider's temperature.
func (f *FallbackProvider) Temperature() float64 {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if sc, ok := active.(SamplingConfigProvider); ok {
		return sc.Temperature()
	}
	return 0
}

// SetTopP forwards to both wrapped providers.
func (f *FallbackProvider) SetTopP(topP float64) {
	f.mu.RLock()
	primary := f.primary
	fallback := f.fallback
	f.mu.RUnlock()
	if sc, ok := primary.(SamplingConfigProvider); ok {
		sc.SetTopP(topP)
	}
	if sc, ok := fallback.(SamplingConfigProvider); ok {
		sc.SetTopP(topP)
	}
}

// TopP returns the active provider's top_p.
func (f *FallbackProvider) TopP() float64 {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if sc, ok := active.(SamplingConfigProvider); ok {
		return sc.TopP()
	}
	return 0
}

// CloneWithModel clones BOTH wrapped providers with the model override and
// returns a new FallbackProvider preserving the failover configuration.
// Named subagents rely on this for their fast/strong model split (#372).
func (f *FallbackProvider) CloneWithModel(model string) Provider {
	f.mu.RLock()
	primary := f.primary
	fallback := f.fallback
	f.mu.RUnlock()

	// Require BOTH providers to support cloning. Half-support (one implements
	// ClonableWithModel, the other doesn't) used to build a wrapper that
	// SHARED the un-cloned instance — the clone's forwarded setters
	// (SetSessionID/SetTemperature/...) then mutated the parent's provider
	// state (#391). Silently keeping the parent (no model override) is the
	// safer degradation.
	pc, pok := primary.(ClonableWithModel)
	fc, fok := fallback.(ClonableWithModel)
	if !pok || !fok {
		return f // cannot clone both sides — keep the wrapper as-is
	}
	primaryClone := pc.CloneWithModel(model)
	fallbackClone := fc.CloneWithModel(model)
	clone := NewFallbackProvider(primaryClone, fallbackClone, f.description)
	clone.SetFailoverNotify(f.notifySnapshot())
	if f.failedOver.Load() {
		clone.failedOver.Store(true)
	}
	return clone
}

// notifySnapshot returns the current notify callback under the read lock.
func (f *FallbackProvider) notifySnapshot() func(FailoverTrigger, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.notify
}

// RateLimitInfo returns the active provider's rate-limit info.
func (f *FallbackProvider) RateLimitInfo() RateLimitInfo {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if rl, ok := active.(RateLimitProvider); ok {
		return rl.RateLimitInfo()
	}
	return RateLimitInfo{}
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
