package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type FailoverTrigger string

const (
	FailoverTriggerQuota            FailoverTrigger = "quota_exhausted"
	FailoverTriggerAuth             FailoverTrigger = "auth_error"
	FailoverTriggerRepeated         FailoverTrigger = "repeated_failures"
	FailoverTriggerBack             FailoverTrigger = "fallback_failed_back_to_primary"
	FailoverTriggerPrimaryRecovered FailoverTrigger = "primary_recovered"
	FailoverTriggerHigherPriority   FailoverTrigger = "higher_priority_recovered"
)

const failoverThreshold = 3

type FallbackProvider struct {
	chain       []Provider
	description string

	mu              sync.RWMutex
	activeIdx       atomic.Int32
	consecutiveFail atomic.Int32

	probeCancel   context.CancelFunc
	probeInterval time.Duration

	notify func(trigger FailoverTrigger, err error)
}

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

func NewFallbackProvider(primary, fallback Provider, description string) *FallbackProvider {
	return NewCascadeProvider([]Provider{primary, fallback}, description)
}

func (f *FallbackProvider) SetFailoverNotify(fn func(trigger FailoverTrigger, err error)) {
	f.mu.Lock()
	f.notify = fn
	f.mu.Unlock()
}

func (f *FallbackProvider) HasFailedOver() bool {
	return f.activeIdx.Load() != 0
}

func (f *FallbackProvider) Reset() {
	f.stopRecoveryProber()
	f.activeIdx.Store(0)
	f.consecutiveFail.Store(0)
}

func (f *FallbackProvider) activeLocked() Provider {
	return f.chain[f.activeIdx.Load()]
}

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

func (f *FallbackProvider) maybeFailover(err error, failedIdx int) (error, bool) {
	if err == nil {

		f.consecutiveFail.Store(0)
		return nil, false
	}

	if len(f.chain) < 2 {
		return err, false
	}

	budgetExhausted := errors.Is(err, errRetryBudgetExhausted)

	if !budgetExhausted && ClassifyLLMError(err) == FailureNone {
		return err, false
	}

	if IsContextOverflowError(err) {
		return err, false
	}

	immediate, trigger := shouldFailover(err)
	if budgetExhausted {
		immediate, trigger = true, FailoverTriggerRepeated
	}

	if !immediate {

		if failedIdx != int(f.activeIdx.Load()) {
			return err, true
		}

		count := f.consecutiveFail.Add(1)
		if count < int32(failoverThreshold) {
			return err, false
		}
		trigger = FailoverTriggerRepeated
	}

	f.mu.Lock()
	cur := int(f.activeIdx.Load())

	if failedIdx != cur {
		f.mu.Unlock()
		return err, true
	}

	next := (cur + 1) % len(f.chain)
	f.activeIdx.Store(int32(next))
	if next != 0 {
		f.startRecoveryProberLocked()
	} else {
		f.stopRecoveryProberLocked()
	}
	if next == 0 && cur != 0 {

		trigger = FailoverTriggerBack
	}
	f.consecutiveFail.Store(0)
	debug.Log("provider", "failover advance: %s level %d -> %d (trigger=%s, error=%v)",
		f.description, cur, next, trigger, err)
	var notify func(FailoverTrigger, error)
	if f.notify != nil {

		notify = f.notify
	}
	f.mu.Unlock()
	if notify != nil {
		notify(trigger, err)
	}
	return err, true
}

func (f *FallbackProvider) Name() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.activeLocked().Name()
}

const defaultProbeInterval = 60 * time.Second

const probeTimeout = 15 * time.Second

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

		defer safego.Recover("provider.fallback.recoveryProber")
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

				f.stopRecoveryProber()
				return
			}

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

func (f *FallbackProvider) stopRecoveryProber() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopRecoveryProberLocked()
}

func (f *FallbackProvider) stopRecoveryProberLocked() {
	if f.probeCancel != nil {
		f.probeCancel()
		f.probeCancel = nil
	}
}

func (f *FallbackProvider) probeLevel(ctx context.Context, idx int) bool {
	f.mu.RLock()
	p := f.chain[idx]
	f.mu.RUnlock()
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := p.Chat(pctx, []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "ping"}}}}, nil)
	return err == nil
}

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

	_, canRetry := f.maybeFailover(err, activeIdx)
	if !canRetry {
		return nil, err
	}

	f.mu.RLock()
	other := f.activeLocked()
	f.mu.RUnlock()

	debug.Log("provider", "Chat failover retry on %s", other.Name())
	resp2, err2 := other.Chat(ctx, messages, tools)
	if err2 == nil {
		f.consecutiveFail.Store(0)
		return resp2, nil
	}

	return nil, errors.Join(err, err2)
}

func (f *FallbackProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	f.mu.RLock()
	activeIdx := int(f.activeIdx.Load())
	active := f.chain[activeIdx]
	f.mu.RUnlock()

	stream, err := active.ChatStream(ctx, messages, tools)
	if err == nil {
		return f.watchStreamForFailover(ctx, activeIdx, stream, messages, tools), nil
	}

	_, canRetry := f.maybeFailover(err, activeIdx)
	if !canRetry {
		return nil, err
	}

	f.mu.RLock()
	newIdx := int(f.activeIdx.Load())
	other := f.activeLocked()
	f.mu.RUnlock()

	debug.Log("provider", "ChatStream failover retry on %s", other.Name())
	stream2, err2 := other.ChatStream(ctx, messages, tools)
	if err2 == nil {

		return f.watchStreamForFailover(ctx, newIdx, stream2, messages, tools), nil
	}

	return nil, errors.Join(err, err2)
}

func (f *FallbackProvider) watchStreamForFailover(ctx context.Context, failedIdx int, stream <-chan StreamEvent, messages []Message, tools []ToolDefinition) <-chan StreamEvent {
	return f.watchStreamForFailoverHops(ctx, failedIdx, stream, messages, tools, len(f.chain))
}

func (f *FallbackProvider) watchStreamForFailoverHops(ctx context.Context, failedIdx int, stream <-chan StreamEvent, messages []Message, tools []ToolDefinition, hopsLeft int) <-chan StreamEvent {
	out := make(chan StreamEvent)
	go func() {

		defer close(out)
		defer safego.Recover("provider.fallback.streamWatcher")
		sawOutput := false
		sawError := false
		// #5193: a clean close resets the consecutive-fail counter even with
		// zero output events - the request cycle succeeded (stream opened
		// and closed without an error event), matching the Chat path's
		// unconditional err==nil reset. Only a stream that DELIVERED an
		// error event keeps the counter alive.
		resetOnSuccess := func() {
			if !sawError {
				f.consecutiveFail.Store(0)
			}
		}

		drainOnAbandon := func() {
			go safego.Run("provider.fallback.drainOnAbandon", func() {
				for range stream {
				}
			})
		}
		_ = drainOnAbandon
		defer drainOnAbandon()
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
				if canRetry && hopsLeft > 0 {
					f.mu.RLock()
					newIdx2 := int(f.activeIdx.Load())
					other := f.activeLocked()
					f.mu.RUnlock()
					debug.Log("provider", "ChatStream async-error failover on %s", other.Name())
					stream2, err2 := other.ChatStream(ctx, messages, tools)
					if err2 == nil {

						go safego.Run("provider.fallback.drain", func() {
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
						})
						if !send(StreamEvent{
							Type: StreamEventSystem,
							Text: fmt.Sprintf("active provider failed (%v); failing over to %s", ev.Error, other.Name()),
						}) {
							return
						}

						watched := f.watchStreamForFailoverHops(ctx, newIdx2, stream2, messages, tools, hopsLeft-1)
						for {
							select {
							case ev2, ok := <-watched:
								if !ok {
									resetOnSuccess()
									return
								}

								if ev2.Type != StreamEventError {
									sawOutput = sawOutput || ev2.Type != StreamEventSystem
								} else {
									sawError = true
								}
								if !send(ev2) {
									return
								}
							case <-ctx.Done():
								return
							}
						}
					}

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
			if ev.Type == StreamEventError {
				sawError = true // delivered-to-consumer error keeps the counter
			}
			if !send(ev) {
				return
			}
		}
	}()
	return out
}

func (f *FallbackProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	return active.CountTokens(ctx, messages)
}

func (f *FallbackProvider) forEach(fn func(p Provider)) {
	for _, p := range f.chain {
		fn(p)
	}
}

func (f *FallbackProvider) ModelName() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if mp, ok := active.(ModelNameProvider); ok {
		return mp.ModelName()
	}
	return ""
}

func (f *FallbackProvider) SetReasoningEffort(effort string) {
	f.forEach(func(p Provider) {
		if rp, ok := p.(ReasoningEffortProvider); ok {
			rp.SetReasoningEffort(effort)
		}
	})
}

func (f *FallbackProvider) ReasoningEffort() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if rp, ok := active.(ReasoningEffortProvider); ok {
		return rp.ReasoningEffort()
	}
	return ""
}

func (f *FallbackProvider) SetTextVerbosity(v string) {
	f.forEach(func(p Provider) {
		if tp, ok := p.(TextVerbosityProvider); ok {
			tp.SetTextVerbosity(v)
		}
	})
}

func (f *FallbackProvider) TextVerbosity() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if tp, ok := active.(TextVerbosityProvider); ok {
		return tp.TextVerbosity()
	}
	return ""
}

func (f *FallbackProvider) SetServiceTier(v string) {
	f.forEach(func(p Provider) {
		if tp, ok := p.(ServiceTierProvider); ok {
			tp.SetServiceTier(v)
		}
	})
}

func (f *FallbackProvider) ServiceTier() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if tp, ok := active.(ServiceTierProvider); ok {
		return tp.ServiceTier()
	}
	return ""
}

func (f *FallbackProvider) SetSessionID(sessionID string) {
	f.forEach(func(p Provider) {
		if ss, ok := p.(SessionIDSetter); ok {
			ss.SetSessionID(sessionID)
		}
	})
}

func (f *FallbackProvider) SetToolChoice(choice string) {
	f.forEach(func(p Provider) {
		if tc, ok := p.(ToolChoiceProvider); ok {
			tc.SetToolChoice(choice)
		}
	})
}

func (f *FallbackProvider) ToolChoice() string {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if tc, ok := active.(ToolChoiceProvider); ok {
		return tc.ToolChoice()
	}
	return ""
}

func (f *FallbackProvider) SetTemperature(temp float64) {
	f.forEach(func(p Provider) {
		if sc, ok := p.(SamplingConfigProvider); ok {
			sc.SetTemperature(temp)
		}
	})
}

func (f *FallbackProvider) Temperature() float64 {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if sc, ok := active.(SamplingConfigProvider); ok {
		return sc.Temperature()
	}
	return 0
}

func (f *FallbackProvider) SetTopP(topP float64) {
	f.forEach(func(p Provider) {
		if sc, ok := p.(SamplingConfigProvider); ok {
			sc.SetTopP(topP)
		}
	})
}

func (f *FallbackProvider) TopP() float64 {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if sc, ok := active.(SamplingConfigProvider); ok {
		return sc.TopP()
	}
	return 0
}

func (f *FallbackProvider) CloneWithModel(model string) Provider {

	clones := make([]Provider, 0, len(f.chain))
	for _, p := range f.chain {
		c, ok := p.(ClonableWithModel)
		if !ok {
			return f
		}
		clones = append(clones, c.CloneWithModel(model))
	}
	clone := NewCascadeProvider(clones, f.description)
	clone.SetFailoverNotify(f.notifySnapshot())
	clone.probeInterval = f.probeInterval
	if cur := f.activeIdx.Load(); cur != 0 {
		clone.activeIdx.Store(cur)

		clone.mu.Lock()
		clone.startRecoveryProberLocked()
		clone.mu.Unlock()
	}
	return clone
}

func (f *FallbackProvider) notifySnapshot() func(FailoverTrigger, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.notify
}

func (f *FallbackProvider) RateLimitInfo() RateLimitInfo {
	f.mu.RLock()
	active := f.activeLocked()
	f.mu.RUnlock()
	if rl, ok := active.(RateLimitProvider); ok {
		return rl.RateLimitInfo()
	}
	return RateLimitInfo{}
}

func (f *FallbackProvider) Description() string {
	return f.description
}

func (f *FallbackProvider) String() string {
	active := int(f.activeIdx.Load())
	return fmt.Sprintf("FallbackProvider(%s, active=%s [level %d])", f.description, f.chain[active].Name(), active)
}
