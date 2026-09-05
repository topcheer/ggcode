package provider

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// RateLimitInfo holds parsed rate-limit metadata from provider response headers.
// Both OpenAI and Anthropic return these on every successful response, enabling
// proactive pacing rather than reactive 429 retry handling.
//
// OpenAI header format (x-ratelimit-*):
//
//	x-ratelimit-limit-requests, x-ratelimit-limit-tokens
//	x-ratelimit-remaining-requests, x-ratelimit-remaining-tokens
//	x-ratelimit-reset-requests, x-ratelimit-reset-tokens
//
// Anthropic header format (anthropic-ratelimit-* or reratelimit-*):
//
//	anthropic-ratelimit-requests-limit, anthropic-ratelimit-requests-remaining
//	anthropic-ratelimit-tokens-limit, anthropic-ratelimit-tokens-remaining
//	anthropic-ratelimit-requests-reset, anthropic-ratelimit-tokens-reset
//
// Newer Anthropic API uses the "reratelimit-" prefix (e.g. reratelimit-requests-remaining).
type RateLimitInfo struct {
	// RemainingRequests is the number of requests left before the rate limit resets.
	// -1 means "unknown" (header not present).
	RemainingRequests int
	// RemainingTokens is the number of tokens left in the current window.
	RemainingTokens int
	// LimitRequests is the max requests per window.
	LimitRequests int
	// LimitTokens is the max tokens per window.
	LimitTokens int
	// ResetRequests is the duration until the request limit resets.
	// Zero means "unknown".
	ResetRequests time.Duration
	// ResetTokens is the duration until the token limit resets.
	ResetTokens time.Duration
	// CapturedAt is when this info was captured.
	CapturedAt time.Time
}

// IsEmpty returns true if no rate-limit headers were found.
func (r RateLimitInfo) IsEmpty() bool {
	return r.RemainingRequests < 0 && r.RemainingTokens < 0 &&
		r.LimitRequests < 0 && r.LimitTokens < 0
}

// RequestFractionRemaining returns the fraction of request quota remaining
// (0.0 to 1.0). Returns 1.0 if unknown.
func (r RateLimitInfo) RequestFractionRemaining() float64 {
	if r.LimitRequests <= 0 || r.RemainingRequests < 0 {
		return 1.0
	}
	f := float64(r.RemainingRequests) / float64(r.LimitRequests)
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// TokenFractionRemaining returns the fraction of token quota remaining
// (0.0 to 1.0). Returns 1.0 if unknown.
func (r RateLimitInfo) TokenFractionRemaining() float64 {
	if r.LimitTokens <= 0 || r.RemainingTokens < 0 {
		return 1.0
	}
	f := float64(r.RemainingTokens) / float64(r.LimitTokens)
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// IsCritical returns true when either quota is below the critical threshold
// (default 10%). The caller should consider pacing or warning the user.
func (r RateLimitInfo) IsCritical() bool {
	return r.RequestFractionRemaining() <= rateLimitCriticalThreshold ||
		r.TokenFractionRemaining() <= rateLimitCriticalThreshold
}

const rateLimitCriticalThreshold = 0.10

// rateLimitTracker stores the latest RateLimitInfo in a thread-safe manner.
// It is embedded in the headerInjectingTransport so that every HTTP response
// automatically updates the tracked rate-limit state.
type rateLimitTracker struct {
	mu   sync.RWMutex
	info RateLimitInfo
}

func newRateLimitTracker() *rateLimitTracker {
	return &rateLimitTracker{
		info: RateLimitInfo{
			RemainingRequests: -1,
			RemainingTokens:   -1,
			LimitRequests:     -1,
			LimitTokens:       -1,
		},
	}
}

// Update parses rate-limit headers from an HTTP response and stores the result.
func (t *rateLimitTracker) Update(header http.Header) {
	if header == nil {
		return
	}
	info := parseRateLimitHeaders(header)
	if info.IsEmpty() {
		return
	}
	info.CapturedAt = time.Now()

	t.mu.Lock()
	t.info = info
	t.mu.Unlock()

	if info.IsCritical() {
		debug.Log("provider", "rate limit critical: req=%d/%d (%.0f%%) tok=%d/%d (%.0f%%) reset_req=%s reset_tok=%s",
			info.RemainingRequests, info.LimitRequests, info.RequestFractionRemaining()*100,
			info.RemainingTokens, info.LimitTokens, info.TokenFractionRemaining()*100,
			info.ResetRequests, info.ResetTokens)
	}
}

// Snapshot returns a copy of the current rate-limit info.
func (t *rateLimitTracker) Snapshot() RateLimitInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.info
}

// parseRateLimitHeaders extracts rate-limit information from HTTP response
// headers. Supports both OpenAI (x-ratelimit-*) and Anthropic
// (anthropic-ratelimit-* / reratelimit-*) header formats.
func parseRateLimitHeaders(h http.Header) RateLimitInfo {
	info := RateLimitInfo{
		RemainingRequests: -1,
		RemainingTokens:   -1,
		LimitRequests:     -1,
		LimitTokens:       -1,
	}

	// Try OpenAI format first (x-ratelimit-*).
	if v := h.Get("x-ratelimit-remaining-requests"); v != "" {
		info.RemainingRequests = parseIntSafe(v, -1)
	}
	if v := h.Get("x-ratelimit-remaining-tokens"); v != "" {
		info.RemainingTokens = parseIntSafe(v, -1)
	}
	if v := h.Get("x-ratelimit-limit-requests"); v != "" {
		info.LimitRequests = parseIntSafe(v, -1)
	}
	if v := h.Get("x-ratelimit-limit-tokens"); v != "" {
		info.LimitTokens = parseIntSafe(v, -1)
	}
	if v := h.Get("x-ratelimit-reset-requests"); v != "" {
		info.ResetRequests = parseDurationSafe(v)
	}
	if v := h.Get("x-ratelimit-reset-tokens"); v != "" {
		info.ResetTokens = parseDurationSafe(v)
	}

	// #624: do NOT early-return when the OpenAI group is non-empty. LiteLLM/
	// OpenRouter-style gateways send their own x-ratelimit-* headers (often
	// only the requests fields) ALONGSIDE the upstream anthropic-ratelimit-*
	// token headers. The old `if !info.IsEmpty() { return info }` here
	// dropped the Anthropic token headers, leaving RemainingTokens=-1 so
	// TokenFractionRemaining() read as 100% and token-critical warnings never
	// fired — the same failure mode #602 R6 fixed for the anthropic/rerate
	// prefix pair. All groups now merge into one info; each field is only
	// written when that specific header exists, so single-group responses
	// parse exactly as before.

	// Try Anthropic format (anthropic-ratelimit-* or reratelimit-*).
	// The Anthropic SDK changed prefix from "anthropic-ratelimit-" to "reratelimit-"
	// in late 2024; we check both for compatibility.
	for _, prefix := range []string{"anthropic-ratelimit-", "reratelimit-"} {
		if v := h.Get(prefix + "requests-remaining"); v != "" {
			info.RemainingRequests = parseIntSafe(v, -1)
		}
		if v := h.Get(prefix + "tokens-remaining"); v != "" {
			info.RemainingTokens = parseIntSafe(v, -1)
		}
		if v := h.Get(prefix + "requests-limit"); v != "" {
			info.LimitRequests = parseIntSafe(v, -1)
		}
		if v := h.Get(prefix + "tokens-limit"); v != "" {
			info.LimitTokens = parseIntSafe(v, -1)
		}
		if v := h.Get(prefix + "requests-reset"); v != "" {
			info.ResetRequests = parseAnthropicResetDuration(v)
		}
		if v := h.Get(prefix + "tokens-reset"); v != "" {
			info.ResetTokens = parseAnthropicResetDuration(v)
		}
		// #602(R6): do NOT early-return after the first non-empty prefix
		// group. A mixed-prefix response (old prefix carrying request
		// headers, new prefix carrying token headers) used to return here
		// with RemainingTokens=-1, so TokenFractionRemaining() read as 100%
		// and token-critical warnings never fired. Both prefixes now merge
		// into one info; each field is only overwritten when that specific
		// header exists, so single-prefix responses parse exactly as before.
	}

	return info
}

// parseIntSafe parses an integer, returning fallback on error.
func parseIntSafe(s string, fallback int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	// Some providers send "Infinity" or non-numeric values.
	n, err := strconv.Atoi(s)
	if err != nil {
		// Try float (e.g., "100.0")
		f, err2 := strconv.ParseFloat(s, 64)
		if err2 != nil {
			return fallback
		}
		// #1617: "Infinity"/"NaN" parse successfully but int() conversion is
		// implementation-defined garbage - remaining counts scrambled and
		// bogus critical alerts.
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return fallback
		}
		return int(f)
	}
	return n
}

// parseDurationSafe parses a Go-style duration string (e.g., "86400000ms",
// "24h0m0s", "1h30m"). Returns zero on failure.
func parseDurationSafe(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Try Go duration format first.
	d, err := time.ParseDuration(s)
	if err == nil {
		// #1617: the bare-ms branch below rejects negatives and clamps to
		// 24h (#664), but the Go-format branch let "-45s"/"876000h" flow
		// straight into the tracker display.
		if d <= 0 {
			return 0
		}
		if d > 24*time.Hour {
			return 24 * time.Hour
		}
		return d
	}
	// Try raw milliseconds (OpenAI sometimes sends "86400000ms" which
	// time.ParseDuration handles, but also bare "86400000").
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		// #664: reject non-positive and clamp oversized values BEFORE the
		// ms→ns multiplication (same family as #513/#658). A bare "-5000"
		// produced a negative duration and ~9.3e12 overflowed negative —
		// both flowed into tracker display as misleading values. This path
		// is display/logging only; values above 24h clamp to 24h.
		if ms <= 0 {
			return 0
		}
		const maxMS = 24 * 60 * 60 * 1000
		if ms > maxMS {
			ms = maxMS
		}
		return time.Duration(ms) * time.Millisecond
	}
	return 0
}

// parseAnthropicResetDuration parses Anthropic's reset header format.
// Anthropic sends values like "8h0m0s", "24h0m0s", or "1h30m0s".
func parseAnthropicResetDuration(s string) time.Duration {
	return parseDurationSafe(s)
}
