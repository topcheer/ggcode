package provider

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// FailureClass categorizes LLM call failures by their recoverability.
// Used by the agent retry logic and by node-level health reporting
// (e.g. lanchat presence) to distinguish "model is broken" from
// "transient hiccup".
type FailureClass int

const (
	// FailureNone means no failure or a non-model failure (nil, cancellation).
	FailureNone FailureClass = iota
	// FailureQuota means permanent quota/billing exhaustion (e.g. coding plan
	// expired, insufficient balance). Sticky — retrying won't help until the
	// user acts. Coding plan providers (ZAI/GLM, Kimi, OpenAI) return 429 for
	// both transient rate limits AND quota exhaustion; keyword matching
	// distinguishes them.
	FailureQuota
	// FailureRateLimit means transient rate limiting (429 without quota
	// keywords, "overloaded"). Typically recovers within minutes.
	FailureRateLimit
	// FailureAuth means authentication/authorization failure (401/403, invalid
	// API key). Sticky — requires user intervention.
	FailureAuth
	// FailureNetwork means transport-level failure (DNS, TCP, TLS, EOF). This
	// says nothing about model health — it may be a local network issue.
	FailureNetwork
	// FailureTransient means other transient server errors (5xx) or unknown
	// failures. Recoverable by retry; not a model health signal.
	FailureTransient
)

// String returns a short stable identifier for the failure class, suitable
// for presence broadcast and logs. FailureNone returns "".
func (c FailureClass) String() string {
	switch c {
	case FailureQuota:
		return "quota"
	case FailureRateLimit:
		return "rate_limited"
	case FailureAuth:
		return "auth"
	case FailureNetwork:
		return "network"
	case FailureTransient:
		return "transient"
	default:
		return ""
	}
}

// quotaKeywords are lowercased substrings indicating permanent quota/billing
// exhaustion. Single source of truth — previously duplicated between
// provider.isQuotaExhaustedError and agent.isAgentQuotaExhausted.
//
// Vendor-specific error types covered:
//   - Kimi/Moonshot: exceeded_current_quota_error, access_terminated
//   - OpenAI: insufficient_quota, quota_exceeded
//   - ZAI/GLM: coding plan, 使用上限, 套餐已到期
//   - Volcengine Ark: QuotaExceeded
//   - Aliyun: allocated quota
//   - MiniMax: usage limit (Anthropic's recoverable 5-hour-window form is
//     excluded via anthropicWindowLimitMarkers before matching, #528)
//   - Xiaomi MiMo: 额度耗尽
var quotaKeywords = []string{
	"coding plan",
	"usage limit",
	"使用上限",
	"套餐已到期",
	"package has expired",
	"insufficient balance",
	"余额不足",
	"欠费",
	"quota exceeded",
	"quotaexceeded",
	"quota_exceeded",
	"insufficient_quota",
	"exceeded_current_quota", // Kimi: exceeded_current_quota_error
	"exceeded your current quota",
	"额度已用完",
	"额度耗尽",
	"配额超限",
	"配额耗尽",
	"allocated quota",
	"公平使用",
	"fair usage",
	"access_terminated",
	// MiniMax: "usage limit exceeded, 5-hour usage limit reached" (permanent
	// until plan reset). Safe to match broadly (#528): Anthropic's
	// recoverable 5-hour-window 429s are excluded earlier by
	// anthropicWindowLimitMarkers ("5-hour window", "limit will reset",
	// "weekly limit") inside isQuotaExhaustedString before this list is
	// consulted, so the bare keyword cannot misfire on them.
	"usage limit",
}

// anthropicWindowLimitMarkers are lowercased substrings that identify
// Anthropic's recoverable usage-limit 429s (rate_limit_error). These reset
// within hours, so they are transient rate limits — NOT permanent quota (#528).
var anthropicWindowLimitMarkers = []string{
	"5-hour window",    // "would exceed your usage limit for the 5-hour window"
	"limit will reset", // "Your limit will reset at ..."
	"weekly limit",     // weekly window variant
}

// isAnthropicWindowLimit reports whether the (lowercased) message carries a
// marker of an auto-resetting Anthropic usage window.
func isAnthropicWindowLimit(s string) bool {
	return containsAny(s, anthropicWindowLimitMarkers)
}

// isQuotaExhaustedString reports whether the (lowercased) message indicates
// permanent quota/billing exhaustion. It is the shared core of
// IsQuotaExhaustedError and ClassifyLLMError.
//
// #528: Anthropic's recoverable 5-hour-window 429 message also contains
// "usage limit", which caused a single rate-limit hit to trigger sticky
// failover. anthropicWindowLimitMarkers ("5-hour window", "limit will reset",
// "weekly limit") are checked first and exclude that form; MiniMax's genuine
// message ("usage limit exceeded, 5-hour usage limit reached") carries no
// marker and remains quota.
func isQuotaExhaustedString(s string) bool {
	if isAnthropicWindowLimit(s) {
		return false
	}
	return containsAny(s, quotaKeywords)
}

// authKeywords are lowercased substrings indicating auth failure.
//
// Bare numeric status substrings ("401") are deliberately excluded (#303):
// they match unrelated numbers in error text (token counts like "requested
// 40123 tokens", request IDs, timestamps) and misroute to FailureAuth, which
// both abandons retry and triggers immediate sticky failover. Status codes
// are matched via context-anchored patterns in authStatusPatterns instead,
// mirroring retry.go's string-fallback checks.
var authKeywords = []string{
	"unauthorized",
	"forbidden",
	"invalid api key",
	"invalid_api_key",
	"incorrect api key",
	"authentication failed",
	"authentication_error",
	"permission denied",
	"access denied",
	"api key is invalid",
	"no auth credentials",
}

// authStatusPatterns are context-anchored "401" matchers so digit
// coincidences ("40123") no longer classify as auth failures (#303).
//
// #313: kept in sync with retry.go's containsHTTPStatus forms — the
// go-openai SDK emits "error, status code: 401, message: ..." (code followed
// by a comma), which the earlier pattern list missed, delaying auth failover
// from immediate to 3-consecutive-failures.
var authStatusPatterns = []string{
	" 401 ",
	" 401,", // go-openai: "status code: 401, message: ..."
	" 401\n",
	"code: 401",
	`status":401`,
	"statuscode:401",
	"http 401",
	"error 401",
	"401 unauthorized",
	"code 401",
}

// rateLimitKeywords are lowercased substrings indicating transient rate
// limiting or server overload (recoverable within minutes).
//
// Vendor-specific error types covered:
//   - Anthropic: overloaded_error (HTTP 529), rate_limit_error (HTTP 429)
//   - Kimi/Moonshot: rate_limit_reached_error, engine_overloaded_error
//   - Google Gemini: RESOURCE_EXHAUSTED (HTTP 429), 503 UNAVAILABLE
//   - OpenAI: rate_limit_exceeded
var rateLimitKeywords = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"overloaded",
	"resource_exhausted",  // Gemini quota/rate limit error type
	"engine_overloaded",   // Kimi engine_overloaded_error
	"rate_limit_exceeded", // OpenAI error type
	"rate_limit_reached",  // Kimi rate_limit_reached_error
}

// rateLimitStatusPatterns are context-anchored "429"/"529" matchers (#456,
// mirroring authStatusPatterns from #303) — bare substrings misclassified
// digit coincidences like "req_429abc", "1429 tokens", "id=52913" as
// rate-limited, triggering wrong failover and health downgrades.
var rateLimitStatusPatterns = []string{
	" 429 ",
	" 429,", // go-openai: "status code: 429, message: ..."
	" 429\n",
	"code: 429",
	`status":429`,
	"statuscode:429",
	"http 429",
	"error 429",
	"429 too many",
	"code 429",
	" 529 ",
	" 529,", // go-openai: "status code: 529, message: ..."
	" 529\n",
	"code: 529",
	`status":529`,
	"http 529",
	"error 529",
	"529 overloaded", // Anthropic overloaded pairs 529 with overloaded_error
	"code 529",
}

// isRateLimitStatusHit reports whether the (lowercased) message contains an
// anchored 429/529 status indicator.
func isRateLimitStatusHit(lower string) bool {
	for _, pat := range rateLimitStatusPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// networkKeywords are lowercased substrings indicating transport-level
// failures that say nothing about model health.
var networkKeywords = []string{
	"connection reset by peer",
	"unexpected eof",
	"broken pipe",
	"tls handshake timeout",
	"server closed idle connection",
	"no such host",
	"connection refused",
	"i/o timeout",
	"eof",
}

// containsAny reports whether s (already lowercased) contains any keyword.
func containsAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// IsQuotaExhaustedError reports whether err indicates permanent quota/billing
// exhaustion. Exported for use by the agent layer and health reporting;
// provider-internal retry logic uses the same definition.
func IsQuotaExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	return isQuotaExhaustedString(strings.ToLower(err.Error()))
}

// ClassifyLLMError categorizes an LLM call failure. Order matters: quota is
// checked before rate limit because quota errors often contain "429" or
// "rate limit" in the message; auth is checked early because some providers
// phrase auth errors ambiguously.
func ClassifyLLMError(err error) FailureClass {
	if err == nil {
		return FailureNone
	}
	if errors.Is(err, context.Canceled) {
		return FailureNone
	}
	// #528: client-side deadlines (long-turn deadline, user re-sending after
	// timeout) say nothing about provider health — same as Canceled (#304's
	// sibling defect). Must NOT count toward the consecutive-failure failover
	// threshold, which only exempts FailureNone.
	// #577(E): BUT a DeadlineExceeded reported through *url.Error IS an HTTP
	// client timeout (net/http always wraps transport/ctx-deadline errors in
	// url.Error) — before this, a pure-timeout endpoint accumulated 0 across
	// 10 real client timeouts and never tripped FailoverTriggerRepeated. Only
	// the unwrapped agent-side sentinel stays exempt; the url.Error-wrapped
	// form counts as a network failure.
	if errors.Is(err, context.DeadlineExceeded) {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return FailureNetwork
		}
		return FailureNone
	}
	// Guard against SDK error types whose .Error() panics on nil internals
	// (e.g. anthropic.Error with nil Response). Same pattern as
	// IsContextOverflowError in retry.go.
	s := strings.ToLower(func() string {
		defer func() { recover() }()
		return err.Error()
	}())

	// #303: check context overflow BEFORE keyword matching — token-count
	// messages ("requested 40123 tokens, maximum is 131072") must reach the
	// compaction path, not FailureAuth/sticky failover.
	if IsContextOverflowError(err) {
		return FailureTransient
	}
	// #528: Anthropic 5-hour-window usage limits auto-reset within hours —
	// transient rate limit, never permanent quota, even though the message
	// contains "usage limit".
	if isAnthropicWindowLimit(s) {
		return FailureRateLimit
	}
	if isQuotaExhaustedString(s) {
		return FailureQuota
	}
	if containsAny(s, authKeywords) || containsAny(s, authStatusPatterns) {
		return FailureAuth
	}
	if containsAny(s, rateLimitKeywords) || isRateLimitStatusHit(s) {
		return FailureRateLimit
	}
	if containsAny(s, networkKeywords) {
		return FailureNetwork
	}
	return FailureTransient
}
