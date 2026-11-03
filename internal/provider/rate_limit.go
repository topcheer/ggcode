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

type RateLimitInfo struct {
	RemainingRequests int
	RemainingTokens   int
	LimitRequests     int
	LimitTokens       int
	ResetRequests     time.Duration
	ResetTokens       time.Duration
	CapturedAt        time.Time
}

func (r RateLimitInfo) IsEmpty() bool {
	return r.RemainingRequests < 0 && r.RemainingTokens < 0 &&
		r.LimitRequests < 0 && r.LimitTokens < 0
}

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

func (r RateLimitInfo) IsCritical() bool {
	return r.RequestFractionRemaining() <= rateLimitCriticalThreshold ||
		r.TokenFractionRemaining() <= rateLimitCriticalThreshold
}

const rateLimitCriticalThreshold = 0.10

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

func (t *rateLimitTracker) Snapshot() RateLimitInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.info
}

func parseRateLimitHeaders(h http.Header) RateLimitInfo {
	info := RateLimitInfo{
		RemainingRequests: -1,
		RemainingTokens:   -1,
		LimitRequests:     -1,
		LimitTokens:       -1,
	}

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

	}

	return info
}

func parseIntSafe(s string, fallback int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}

	n, err := strconv.Atoi(s)
	if err != nil {

		f, err2 := strconv.ParseFloat(s, 64)
		if err2 != nil {
			return fallback
		}

		if math.IsInf(f, 0) || math.IsNaN(f) {
			return fallback
		}
		return int(f)
	}
	return n
}

func parseDurationSafe(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	d, err := time.ParseDuration(s)
	if err == nil {

		if d <= 0 {
			return 0
		}
		if d > 24*time.Hour {
			return 24 * time.Hour
		}
		return d
	}

	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {

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

func parseAnthropicResetDuration(s string) time.Duration {
	return parseDurationSafe(s)
}
