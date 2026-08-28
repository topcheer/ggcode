package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// FailoverTrigger is the reason a failover was activated.
type FailoverTrigger string

const (
	FailoverTriggerQuota    FailoverTrigger = "quota_exhausted"
	FailoverTriggerAuth     FailoverTrigger = "auth_error"
	FailoverTriggerRepeated FailoverTrigger = "repeated_failures"
	// FailoverTriggerBack fires when the LAST provider in the chain failed
	// hard and the wrapper wrapped around to the primary (index 0). Without
	// this, a chain exhausting its own quota/auth limits left the app
	// permanently unusable - every call errored with no recovery path short
	// of manual /model.
	FailoverTriggerBack FailoverTrigger = "fallback_failed_back_to_primary"
	// FailoverTriggerPrimaryRecovered fires when the background recovery
	// prober found the PRIMARY (index 0) healthy again and switched back
	// proactively - no failure on the active side was needed.
	FailoverTriggerPrimaryRecovered FailoverTrigger = "primary_recovered"
	// FailoverTriggerHigherPriority fires when the recovery prober found an
	// intermediate chain level healthy again (not the primary, but higher
	// priority than the currently active one).
	FailoverTriggerHigherPriority FailoverTrigger = "higher_priority_recovered"
)

// failoverThreshold is the number of consecutive transient failures on the
// active provider before advancing to the next in the chain. We allow a few
// retries because transient hiccups are common, but sustained failure means
// the provider is effectively down.
const failoverThreshold = 3

// FallbackProvider wraps a priority-ordered chain of providers (index 0 is
// the primary) and transparently advances down the chain on permanent errors
// (quota exhaustion, auth failure) or sustained transient failures.
//
// Chain semantics:
//   - On permanent failures (quota/auth), advancement is immediate. On
//     transient failures (rate limit, 5xx, network), the active provider is
//     retried up to failoverThreshold consecutive times before advancing.
//   - Advancement wraps: when the LAST chain level fails, the next attempt
//     goes back to the primary (index 0). Each individual call bounds itself
//     to one advancement step, so an all-levels-down outage costs one failed
//     request per level per call rather than an infinite in-call loop.
//   - A background recovery prober runs whenever the active level is not the
//     primary. It probes the chain from index 0 DOWNWARD and switches to the
//     FIRST healthy level - the primary always wins recovery priority, so a
//     healthy primary is returned to directly even from the last level,
//     without lingering on intermediate levels.
//   - A StreamEventSystem notification is emitted on every switch, so the
//     user understands why output may look different.
type FallbackProvider struct {
	// chain is the priority-ordered provider list; chain[0] is the primary.
	// Immutable after construction (len >= 1; len == 1 disables failover).
	chain []Provider
	// description is a human-readable label,
	// e.g. "zai/glm-4.6 -> kimi/k2 -> anthropic/claude-sonnet".
	description string

	mu              sync.RWMutex
	activeIdx       atomic.Int32
	consecutiveFail atomic.Int32

	// probeCancel stops the recovery prober (nil when not running).
	probeCancel context.CancelFunc
	// probeInterval overrides the probe tick (tests only; 0 = default).
	probeInterval time.Duration

	// notify is called when a failover occurs, so the UI can inform the user.
	// It receives the trigger reason and the error that caused it.
	notify func(trigger FailoverTrigger, err error)
}

// NewCascadeProvider creates a provider chain that tries chain[0] first and
// advances down the list on permanent or sustained failures, wrapping back
// to chain[0] after the last level fails. Providers after the first are
// tried in the given order (earlier = higher priority).
func NewCascadeProvider(chain []Provider, description string) *FallbackProvider {
	if len(chain) == 0 {
		panic("NewCascadeProvider: empty chain")
	}
	if description == "" {
		description = fmt.Sprintf("cascade(%d)", len(chain))
	}
	return &FallbackProvider{
		chain:       chain,
		description: description,
	}
}

// NewFallbackProvider creates a two-level chain: primary first, fallback on
// permanent or sustained failures. Kept for compatibility with the classic
// single-fallback configuration.
func NewFallbackProvider(primary, fallback Provider, description string) *FallbackProvider {
	return NewCascadeProvider([]Provider{primary, fallback}, description)
}

// SetFailoverNotify sets a callback invoked when failover is activated.
func (f *FallbackProvider) SetFailoverNotify(fn func(trigger FailoverTrigger, err error)) {
	f.mu.Lock()
	f.notify = fn
	f.mu.Unlock()
}

// HasFailedOver reports whether the active provider is not the primary.
func (f *FallbackProvider) HasFailedOver() bool {
	return f.activeIdx.Load() != 0
}

// Reset clears the failover state, returning to the primary provider.
// Called when the user manually switches models/providers.
func (f *FallbackProvider) Reset() {
	f.stopRecoveryProber()
	f.activeIdx.Store(0)
	f.consecutiveFail.Store(0)
}

// activeLocked returns the currently active provider (must hold f.mu).
func (f *FallbackProvider) activeLocked() Provider {
	return f.chain[f.activeIdx.Load()]
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

// maybeFailover checks the error and decides whether to advance down the
// chain. Returns the error to return to the caller (nil if we should retry
// elsewhere) and true if a retry on the (new) active provider is possible.
//
// Wrap-around (#936): when the LAST chain level is active and it fails hard,
// the wrapper advances back to the primary. Levels can cycle - each call
// still bounds itself to one advancement, so an all-down outage costs one
// failed request per level per call rather than an infinite in-call loop.
//
// Must NOT be called under f.mu - this function acquires the write lock.
func (f *FallbackProvider) maybeFailover(err error, failedIdx int) (error, bool) {
	if err == nil {
		// Success - reset consecutive failure counter.
		f.consecutiveFail.Store(0)
		return nil, false
	}

	// Single-level chains cannot fail over anywhere.
	if len(f.chain) < 2 {
		return err, false
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
	// but the counter below consumed every non-quota/auth error - 3
	// consecutive cancels on the non-streaming path would sticky-failover a
	// healthy primary for the rest of the session.
	if !budgetExhausted && ClassifyLLMError(err) == FailureNone {
		return err, false
	}
	// #303: context overflow is a request-size problem handled by agent-side
	// compaction, not provider health - don't count it toward failover either.
	if IsContextOverflowError(err) {
		return err, false
	}

	immediate, trigger := shouldFailover(err)
	if budgetExhausted {
		immediate, trigger = true, FailoverTriggerRepeated
	}

	if !immediate {
		// Transient error - increment counter and check threshold.
		count := f.consecutiveFail.Add(1)
		if count < int32(failoverThreshold) {
			return err, false
		}
		trigger = FailoverTriggerRepeated
	}

	f.mu.Lock()
	cur := int(f.activeIdx.Load())

	// Stale read (#164): the caller grabbed the OLD active before an earlier
	// failure already advanced the chain. It has never touched the current
	// active and still deserves one retry - without switching again.
	if failedIdx != cur {
		f.mu.Unlock()
		return err, true
	}

	// The failed call was on the current active - advance one step (wrapping
	// back to the primary after the last level, #936).
	next := (cur + 1) % len(f.chain)
	f.activeIdx.Store(int32(next))
	if next != 0 {
		f.startRecoveryProberLocked()
	} else {
		f.stopRecoveryProberLocked()
	}
	if next == 0 && cur != 0 {
		// Wrapped from the last level back to the primary.
		trigger = FailoverTriggerBack
	}
	f.consecutiveFail.Store(0)
	debug.Log("provider", "failover advance: %s level %d -> %d (trigger=%s, error=%v)",
		f.description, cur, next, trigger, err)
	var notify func(FailoverTrigger, error)
	if f.notify != nil {
		// Copy callback under lock to avoid race with SetFailoverNotify.
		notify = f.notify
	}
	f.mu.Unlock()
	if notify != nil {
		notify(trigger, err)
	}
	return err, true
}

// Name returns the name of the currently active provider.
func (f *FallbackProvider) Name() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.activeLocked().Name()
}

// defaultProbeInterval is how often the recovery prober checks higher-priority
// chain levels while a lower-priority one is active. A real (tiny) request is
// the only reliable health signal - quota/auth state lives server-side, so
// this cannot be inferred locally. 60s bounds wasted probe tokens while still
// switching back within a minute of recovery in the common case.
const defaultProbeInterval = 60 * time.Second

// probeTimeout bounds a single probe request.
const probeTimeout = 15 * time.Second

// startRecoveryProberLocked launches the background recovery prober (must
// hold f.mu). Idempotent: if a prober is already running this is a no-op.
//
// Recovery priority is PRIMARY-FIRST: on every tick the prober probes the
// chain from index 0 downward and switches to the FIRST healthy level. A
// healthy primary therefore always wins - even from the last level, the
// wrapper returns straight to index 0 without lingering on intermediate
// levels. The prober keeps running while the active level is not the primary
// (an intermediate level that recovered may itself be superseded when the
// primary recovers later), and retires itself the moment index 0 is active.
func (f *FallbackProvider) startRecoveryProberLocked() {
	if f.probeCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.probeCancel = cancel
	interval := f.probeInterval
	if interval <= 0 {
		interval = defaultProbeInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			active := int(f.activeIdx.Load())
			if active == 0 {
				// Someone else already returned to the primary (manual Reset,
				// failure wrap-around) - retire this prober.
				f.stopRecoveryProber()
				return
			}
			// Probe from the primary downward; first healthy level wins.
			target := -1
			for i := 0; i < active; i++ {
				if f.probeLevel(ctx, i) {
					target = i
					break
				}
			}
			if target < 0 {
				continue
			}
			var trigger FailoverTrigger
			if target == 0 {
				trigger = FailoverTriggerPrimaryRecovered
			} else {
				trigger = FailoverTriggerHigherPriority
			}
			f.mu.Lock()
			cur := int(f.activeIdx.Load())
			if cur == target {
				// Someone else already switched here.
				f.stopRecoveryProberLocked()
				f.mu.Unlock()
				return
			}
			f.activeIdx.Store(int32(target))
			f.consecutiveFail.Store(0)
			if target == 0 {
				f.stopRecoveryProberLocked()
			}
			debug.Log("provider", "recovery: %s level %d -> %d", f.description, cur, target)
			var notify func(FailoverTrigger, error)
			if f.notify != nil {
				notify = f.notify
			}
			f.mu.Unlock()
			if notify != nil {
				notify(trigger, nil)
			}
			if target == 0 {
				return
			}
		}
	}()
}

// stopRecoveryProber cancels and clears the prober (safe unlocked).
func (f *FallbackProvider) stopRecoveryProber() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopRecoveryProberLocked()
}

// stopRecoveryProberLocked cancels and clears the prober (must hold f.mu).
func (f *FallbackProvider) stopRecoveryProberLocked() {
	if f.probeCancel != nil {
		f.probeCancel()
		f.probeCancel = nil
	}
}

// probeLevel sends one minimal Chat request to the given chain level. A nil
// error is the only health signal we trust (quota/auth state is server-side).
func (f *FallbackProvider) probeLevel(ctx context.Context, idx int) bool {
	f.mu.RLock()
	p := f.chain[idx]
	f.mu.RUnlock()
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := p.Chat(pctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "ping"}}}}, nil)
	return err == nil
}

// Chat sends a non-streaming request, failing over on permanent errors.
func (f *FallbackProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	f.mu.RLock()
	activeIdx := int(f.activeIdx.Load())
	active := f.chain[activeIdx]
	f.mu.RUnlock()

	resp, err := active.Chat(ctx, messages, tools)
	if err == nil {
		f.consecutiveFail.Store(0)
		return resp, nil
	}

	// Check if we should failover.
	_, canRetry := f.maybeFailover(err, activeIdx)
	if !canRetry {
		return nil, err
	}

	// Retry on the (possibly advanced) active provider.
	f.mu.RLock()
	other := f.activeLocked()
	f.mu.RUnlock()

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
// Mid-stream errors are NOT retried - partial output has already been sent.
//
// All bundled providers return their channel immediately and report
// quota/auth/connection failures as ASYNC StreamEventError events, so the
// sync-error path alone never fires for them. The returned channel is
// therefore wrapped: the first Error event observed before any text arrives
// is routed through maybeFailover, and when it trips the failover the next
// provider's stream is transparently substituted (fresh request, no partial
// output lost). Errors after output has started still pass through
// unchanged (#371).
func (f *FallbackProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	f.mu.RLock()
	activeIdx := int(f.activeIdx.Load())
	active := f.chain[activeIdx]
	f.mu.RUnlock()

	stream, err := active.ChatStream(ctx, messages, tools)
	if err == nil {
		return f.watchStreamForFailover(ctx, activeIdx, stream, messages, tools), nil
	}

	// Check if we should failover.
	_, canRetry := f.maybeFailover(err, activeIdx)
	if !canRetry {
		return nil, err
	}

	// Retry on the (possibly advanced) active provider (#936 wrap-around).
	f.mu.RLock()
	newIdx := int(f.activeIdx.Load())
	other := f.activeLocked()
	f.mu.RUnlock()

	debug.Log("provider", "ChatStream failover retry on %s", other.Name())
	stream2, err2 := other.ChatStream(ctx, messages, tools)
	if err2 == nil {
		// #1164: watch the retried stream exactly like the primary path above.
		// Bundled providers return their channel immediately and surface
		// quota/auth failures as async error events - a bare return here
		// dropped chained failover for the entire retry (a failed-over call
		// stuck on an async-broken second provider never reached the next
		// one). The watcher's resetOnSuccess carries the success-time counter
		// reset instead of an eager Store(0), keeping primary-path semantics.
		return f.watchStreamForFailover(ctx, newIdx, stream2, messages, tools), nil
	}
	// #454: same root-cause preservation as Chat - join both errors so the
	// primary's quota/auth cause is not masked by the fallback's network error.
	return nil, errors.Join(err, err2)
}

// watchStreamForFailover relays a provider's stream while watching the first
// error-before-output event for failover-worthy failures. Once any
// text/output event has been relayed, errors pass through unchanged (partial
// output must not be retried). When failover activates before output, the
// next provider's stream replaces the remainder of this one.
func (f *FallbackProvider) watchStreamForFailover(ctx context.Context, failedIdx int, stream <-chan StreamEvent, messages []Message, tools []ToolDefinition) <-chan StreamEvent {
	return f.watchStreamForFailoverHops(ctx, failedIdx, stream, messages, tools, len(f.chain))
}

// watchStreamForFailoverHops is watchStreamForFailover with a hop budget.
// hopsLeft bounds how many more async failovers this stream may chain: every
// async hop advances the chain, so after len(chain) hops each provider has
// had one chance for this call — beyond that the error is relayed instead of
// recursing through a fully-broken chain forever (#1244).
func (f *FallbackProvider) watchStreamForFailoverHops(ctx context.Context, failedIdx int, stream <-chan StreamEvent, messages []Message, tools []ToolDefinition, hopsLeft int) <-chan StreamEvent {
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		sawOutput := false
		resetOnSuccess := func() {
			if sawOutput {
				// A stream that delivered output and ended cleanly proves the
				// provider works - reset the transient-failure streak, same
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
		// buffered channel fill - three stuck layers leaked per cancelled
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
				_, canRetry := f.maybeFailover(ev.Error, failedIdx)
				// canRetry alone decides, matching the sync Chat/ChatStream
				// paths: maybeFailover returns true both when it JUST
				// advanced the chain and when the failed call raced an
				// earlier advancement (#164 retry-once). The old
				// `&& !f.failedOver.Load()` gate made this branch dead code -
				// canRetry=true always co-occurs with an advancement (#390).
				// #1244: hopsLeft bounds the chain (see wrapper). Before this, the
				// replacement stream was relayed bare — a second provider failing
				// async pre-output terminated the chain here, so P→B both
				// async-broken never reached C even though the chain had it.
				if canRetry && hopsLeft > 0 {
					f.mu.RLock()
					newIdx2 := int(f.activeIdx.Load())
					other := f.activeLocked()
					f.mu.RUnlock()
					debug.Log("provider", "ChatStream async-error failover on %s", other.Name())
					stream2, err2 := other.ChatStream(ctx, messages, tools)
					if err2 == nil {
						// Drain the failed stream and relay the next one.
						// #602(R2): the drain must be cancellable too - a
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
						// #1244: the replacement stream gets the SAME async-error
						// failover treatment (recursively, one hop cheaper) instead
						// of the bare relay loop this used to be.
						watched := f.watchStreamForFailoverHops(ctx, newIdx2, stream2, messages, tools, hopsLeft-1)
						for {
							select {
							case ev2, ok := <-watched:
								if !ok {
									resetOnSuccess()
									return
								}
								// #577(D): ANY content event (text, reasoning,
								// tool-call, done) proves the fallback delivered -
								// not just text. Counting text alone left pure
								// tool-call/reasoning successes from clearing
								// consecutiveFail, so stale counts prematurely
								// failed over a healthy primary (#376 semantics).
								// Mirrors the primary-stream rule at the bottom of
								// this loop. System notices from deeper hops still
								// count as non-output.
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
					// Fallback stream could not even start. #1163: preserve
					// BOTH root causes like the sync Chat/ChatStream paths do
					// with errors.Join - silently dropping the fallback's own
					// sync-start failure misreported the root cause (the
					// consumer saw the primary's stale error) and skewed
					// failover accounting for a call whose replacement never ran.
					evOut := ev
					if err2 != nil {
						evOut.Error = errors.Join(ev.Error, err2)
					}
					if !send(evOut) {
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

// forEach applies fn to every provider in the chain (must hold no lock; the
// chain is immutable after construction).
func (f *FallbackProvider) forEach(fn func(p Provider)) {
	for _, p := range f.chain {
		fn(p)
	}
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

// SetReasoningEffort sets the effort on all chain providers that support it.
func (f *FallbackProvider) SetReasoningEffort(effort string) {
	f.forEach(func(p Provider) {
		if rp, ok := p.(ReasoningEffortProvider); ok {
			rp.SetReasoningEffort(effort)
		}
	})
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

// SetSessionID sets the session ID on all chain providers that support it.
func (f *FallbackProvider) SetSessionID(sessionID string) {
	f.forEach(func(p Provider) {
		if ss, ok := p.(SessionIDSetter); ok {
			ss.SetSessionID(sessionID)
		}
	})
}

// --- Optional interface forwarding (#372) ---
//
// The agent (and subagent runner) probe the provider for these optional
// capabilities via type assertions. Without forwarding them, wrapping a
// provider in FallbackProvider silently disabled tool_choice, adaptive
// sampling, rate-limit warnings, and - most damaging - named-subagent model
// overrides (CloneWithModel fell back to "keep the original provider").

// SetToolChoice forwards to all chain providers (per-call state that must
// stay in sync regardless of which one is active).
func (f *FallbackProvider) SetToolChoice(choice string) {
	f.forEach(func(p Provider) {
		if tc, ok := p.(ToolChoiceProvider); ok {
			tc.SetToolChoice(choice)
		}
	})
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

// SetTemperature forwards to all chain providers.
func (f *FallbackProvider) SetTemperature(temp float64) {
	f.forEach(func(p Provider) {
		if sc, ok := p.(SamplingConfigProvider); ok {
			sc.SetTemperature(temp)
		}
	})
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

// SetTopP forwards to all chain providers.
func (f *FallbackProvider) SetTopP(topP float64) {
	f.forEach(func(p Provider) {
		if sc, ok := p.(SamplingConfigProvider); ok {
			sc.SetTopP(topP)
		}
	})
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

// CloneWithModel clones ALL chain providers with the model override and
// returns a new FallbackProvider preserving the failover configuration.
// Named subagents rely on this for their fast/strong model split (#372).
func (f *FallbackProvider) CloneWithModel(model string) Provider {
	// Require EVERY provider to support cloning. Partial support (some
	// implement ClonableWithModel, some don't) used to build a wrapper that
	// SHARED the un-cloned instance - the clone's forwarded setters
	// (SetSessionID/SetTemperature/...) then mutated the parent's provider
	// state (#391). Silently keeping the parent (no model override) is the
	// safer degradation.
	clones := make([]Provider, 0, len(f.chain))
	for _, p := range f.chain {
		c, ok := p.(ClonableWithModel)
		if !ok {
			return f // cannot clone every level - keep the wrapper as-is
		}
		clones = append(clones, c.CloneWithModel(model))
	}
	clone := NewCascadeProvider(clones, f.description)
	clone.SetFailoverNotify(f.notifySnapshot())
	clone.probeInterval = f.probeInterval
	if cur := f.activeIdx.Load(); cur != 0 {
		clone.activeIdx.Store(cur)
		// The clone inherits the advanced state, so it must also inherit the
		// recovery prober - otherwise a subagent's clone would never
		// proactively return to higher-priority levels when they recover.
		clone.mu.Lock()
		clone.startRecoveryProberLocked()
		clone.mu.Unlock()
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
	active := int(f.activeIdx.Load())
	return fmt.Sprintf("FallbackProvider(%s, active=%s [level %d])", f.description, f.chain[active].Name(), active)
}
