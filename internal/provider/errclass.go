package provider

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

type FailureClass int

const (
	FailureNone FailureClass = iota
	FailureQuota
	FailureRateLimit
	FailureAuth
	FailureNetwork
	FailureTransient
)

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

var anthropicWindowLimitMarkers = []string{
	"5-hour window",
	"limit will reset",
	"weekly limit",
}

var nonAnthropicQuotaCodes = []string{
	"insufficient_quota",
	"exceeded_current_quota",
	"quota_exceeded",
	"quotaexceeded",
	"quota exceeded",
}

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

func isAnthropicWindowLimit(s string) bool {
	if containsAny(s, nonAnthropicQuotaCodes) {
		return false
	}

	if containsAny(s, chineseQuotaMarkers) {
		return false
	}
	return containsAny(s, anthropicWindowLimitMarkers)
}

func isQuotaExhaustedString(s string) bool {
	if isAnthropicWindowLimit(s) {
		return false
	}
	return containsAny(s, quotaKeywords)
}

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

func isRateLimitStatusHit(lower string) bool {
	return containsAnyAnchored(lower, rateLimitStatusPatterns)
}

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

func containsAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

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

func containsAnyAnchored(s string, pats []string) bool {
	for _, p := range pats {
		if containsPatternAnchored(s, p) {
			return true
		}
	}
	return false
}

func IsQuotaExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	return isQuotaExhaustedString(strings.ToLower(err.Error()))
}

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
