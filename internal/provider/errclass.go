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
	"exceeded_current_quota",
	"exceeded your current quota",
	"额度已用完",
	"额度耗尽",
	"配额超限",
	"配额耗尽",
	"allocated quota",
	"公平使用",
	"fair usage",
	"access_terminated",

	"usage limit",
}

// anthropicWindowLimitMarkers are lowercased substrings that identify
// Anthropic's recoverable usage-limit 429s (rate_limit_error). These reset
// within hours, so they are transient rate limits — NOT permanent quota (#528).
var anthropicWindowLimitMarkers = []string{
	"5-hour window",
	"limit will reset",
	"weekly limit",
}

// nonAnthropicQuotaCodes are quota error codes emitted by non-Anthropic
// vendors (OpenAI: insufficient_quota; Kimi: exceeded_current_quota;
// Volcengine Ark: QuotaExceeded). Their presence identifies the error
// surface as non-Anthropic, so the Anthropic window-limit exclusion must
// not apply even when the message also carries a generic "limit will
// reset" phrase (#602 R4): OpenAI's billing-cycle message
// "insufficient_quota: usage limit exceeded; your limit will reset at ..."
// is a permanent quota failure, not a recoverable transient rate limit.
// Before the vendor gate it classified as FailureRateLimit and was
// retried 20 times, all doomed.
var nonAnthropicQuotaCodes = []string{
	"insufficient_quota",
	"exceeded_current_quota",
	"quota_exceeded",
	"quotaexceeded",
	"quota exceeded",
}

// chineseQuotaMarkers are strong Chinese quota-exhaustion keywords (#708
// folded LOW): a non-Anthropic vendor emitting an Anthropic-style message
// with "limit will reset" plus Chinese quota wording is a permanent quota
// failure, not a recoverable window limit — veto the Anthropic exclusion
// when any of these appear.
var chineseQuotaMarkers = []string{
	"使用上限",
	"配额超限",
	"配额耗尽",
	"额度已用完",
	"额度耗尽",
	"余额不足",
	"套餐已到期",
	"欠费",
}

// isAnthropicWindowLimit reports whether the (lowercased) message carries
// a marker of an auto-resetting Anthropic usage window. #602(R4): only for
// error surfaces that do not carry a non-Anthropic quota error code — the
// exclusion describes Anthropic's recoverable 5-hour/weekly windows, not
// OpenAI-style billing cycles that merely mention a reset time.
func isAnthropicWindowLimit(s string) bool {
	if containsAny(s, nonAnthropicQuotaCodes) {
		return false
	}

	if containsAny(s, chineseQuotaMarkers) {
		return false
	}
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
	" 401,",
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
	"resource_exhausted",
	"engine_overloaded",
	"rate_limit_exceeded",
	"rate_limit_reached",
}

// rateLimitStatusPatterns are context-anchored "429"/"529" matchers (#456,
// mirroring authStatusPatterns from #303) — bare substrings misclassified
// digit coincidences like "req_429abc", "1429 tokens", "id=52913" as
// rate-limited, triggering wrong failover and health downgrades.
var rateLimitStatusPatterns = []string{
	" 429 ",
	" 429,",
	" 429\n",
	"code: 429",
	`status":429`,
	"statuscode:429",
	"http 429",
	"error 429",
	"429 too many",
	"code 429",
	" 529 ",
	" 529,",
	" 529\n",
	"code: 529",
	`status":529`,
	"http 529",
	"error 529",
	"529 overloaded",
	"code 529",
}

// isRateLimitStatusHit reports whether the (lowercased) message contains an
// anchored 429/529 status indicator. #602(R3): digit-terminated patterns
// (e.g. `status":429`) require a trailing non-digit boundary so a 5-digit
// coincidence like `"status":42999` cannot pierce the prefix match.
func isRateLimitStatusHit(lower string) bool {
	return containsAnyAnchored(lower, rateLimitStatusPatterns)
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

// containsPatternAnchored reports whether s contains pat. When pat ENDS in
// ASCII digits, the character right after the match in s must be a non-digit
// (or end-of-string). #602(R3): the JSON anchors `status":401` /
// `status":429` / `status":529` digit-pierced — a 5-digit coincidence like
// `"status":40139` matched the "401" prefix and misclassified as
// FailureAuth (and "42999" as FailureRateLimit), triggering sticky auth
// failover for the rest of the session (#303/#456/#577 anchor-hardening
// closure). Patterns that do not end in digits (e.g. " 401,") are already
// self-delimiting and match via plain Contains.
func containsPatternAnchored(s, pat string) bool {
	i := len(pat)
	for i > 0 && pat[i-1] >= '0' && pat[i-1] <= '9' {
		i--
	}
	head, digits := pat[:i], pat[i:]
	if digits == "" {
		return strings.Contains(s, pat)
	}
	from := 0
	for from+len(pat) <= len(s) {
		j := strings.Index(s[from:], head)
		if j < 0 {
			return false
		}
		start := from + j
		end := start + len(pat)
		if s[start+len(head):end] == digits &&
			(end >= len(s) || s[end] < '0' || s[end] > '9') {
			return true
		}
		from = start + 1
	}
	return false
}

// containsAnyAnchored applies containsPatternAnchored over a pattern list.
func containsAnyAnchored(s string, pats []string) bool {
	for _, p := range pats {
		if containsPatternAnchored(s, p) {
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

	if errors.Is(err, context.DeadlineExceeded) {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return FailureNetwork
		}
		return FailureNone
	}

	s := strings.ToLower(func() string {
		defer func() { recover() }()
		return err.Error()
	}())

	if IsContextOverflowError(err) {
		return FailureTransient
	}

	if isAnthropicWindowLimit(s) {
		return FailureRateLimit
	}
	if isQuotaExhaustedString(s) {
		return FailureQuota
	}

	if containsAny(s, authKeywords) || containsAnyAnchored(s, authStatusPatterns) {
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
