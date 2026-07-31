package provider

import (
	"context"
	"errors"
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
//   - MiniMax: usage limit
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
}

// authKeywords are lowercased substrings indicating auth failure.
var authKeywords = []string{
	"401",
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
	"429",
	"overloaded",
	"529",                 // Anthropic overloaded (non-standard HTTP status)
	"resource_exhausted",  // Gemini quota/rate limit error type
	"engine_overloaded",   // Kimi engine_overloaded_error
	"rate_limit_exceeded", // OpenAI error type
	"rate_limit_reached",  // Kimi rate_limit_reached_error
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
	return containsAny(strings.ToLower(err.Error()), quotaKeywords)
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
	s := strings.ToLower(err.Error())

	if containsAny(s, quotaKeywords) {
		return FailureQuota
	}
	if containsAny(s, authKeywords) {
		return FailureAuth
	}
	if containsAny(s, rateLimitKeywords) {
		return FailureRateLimit
	}
	if containsAny(s, networkKeywords) {
		return FailureNetwork
	}
	return FailureTransient
}
