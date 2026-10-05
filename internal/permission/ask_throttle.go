package permission

import (
	"encoding/json"
	"sync"
	"time"
)

// AskThrottle detects and blocks approval-fatigue request flooding
// (ATR-2026-00118 attack pattern 1: rapid repeated permission requests until
// the user mechanically approves). ApprovalMemory already remembers denials
// as input to FUTURE auto-approval decisions, but it does not stop the agent
// from re-prompting the user for the same key it just denied - in a long
// autopilot run, the Nth identical popup gets a reflexive "y", which is
// indistinguishable from real consent.
//
// Policy: the same permission key (MakeKey semantics - tool + path/command
// signature, so argument jitter does not reset the window) that was denied
// denyThreshold times within window is suppressed: the call is denied
// outright WITHOUT prompting the user again, and the denial message steers
// the model toward changing its approach instead of re-asking.
//
// This is deliberately NOT a security boundary (the policy gate and sandbox
// own that); it is an attention-conservation circuit breaker on the human
// gate itself.
type AskThrottle struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	denials   map[string][]time.Time
}

// DefaultAskThrottleWindow / DefaultAskThrottleThreshold: two denials of the
// same key within one minute trips the breaker.
const (
	DefaultAskThrottleWindow    = time.Minute
	DefaultAskThrottleThreshold = 2
)

func NewAskThrottle() *AskThrottle {
	return &AskThrottle{
		window:    DefaultAskThrottleWindow,
		threshold: DefaultAskThrottleThreshold,
		denials:   make(map[string][]time.Time, 8),
	}
}

func (t *AskThrottle) key(toolName string, input json.RawMessage) string {
	k, _ := MakeKey(toolName, input)
	return k
}

// RecordDenial records that the user denied the key. Call on every Deny
// response BEFORE returning the denial result.
func (t *AskThrottle) RecordDenial(toolName string, input json.RawMessage) {
	if t == nil {
		return
	}
	k := t.key(toolName, input)
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.denials[k] = append(t.prune(t.denials[k], now), now)
}

// ShouldSuppress reports whether this key has hit the denial threshold
// within the window: a further Ask would be approval-fatigue flooding and
// must be answered with an immediate deny (message steers the model to
// change approach) instead of prompting the user again.
func (t *AskThrottle) ShouldSuppress(toolName string, input json.RawMessage) bool {
	if t == nil {
		return false
	}
	k := t.key(toolName, input)
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.prune(t.denials[k], now)) >= t.threshold
}

// prune drops entries older than the window; caller holds mu.
func (t *AskThrottle) prune(times []time.Time, now time.Time) []time.Time {
	kept := times[:0]
	for _, at := range times {
		if now.Sub(at) <= t.window {
			kept = append(kept, at)
		}
	}
	return kept
}
