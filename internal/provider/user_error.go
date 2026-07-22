package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

// UserFacingError translates a technical provider/API error into a concise,
// human-readable message in Chinese.
//
// Deprecated: Use UserFacingErrorLang for language-aware error messages.
// This function is kept for backward compatibility with callers that don't
// have a language context (e.g. tunnel_host, daemon_bridge).
func UserFacingError(err error) string {
	return UserFacingErrorLang(err, "zh-CN")
}

// UserFacingErrorLang translates a technical provider/API error into a concise,
// human-readable message in the specified language ("zh-CN" or "en").
//
// It strips SDK-specific noise, maps HTTP status codes to friendly text, and
// falls back to a generic message so users never see raw stack traces or
// protocol details.
func UserFacingErrorLang(err error, lang string) string {
	if err == nil {
		return ""
	}

	// Unwrap once to handle our own fmt.Errorf("xxx: %w", err) wrappers.
	inner := errors.Unwrap(err)
	if inner == nil {
		inner = err
	}

	zh := lang != "en" // default to Chinese for anything other than "en"

	// ---- Auth / permission errors (never retried) ----
	if hasHTTPStatus(err, http.StatusUnauthorized) {
		if zh {
			return "API 密钥无效或未配置，请检查你的 API Key 设置"
		}
		return "Invalid or missing API key. Please check your API key configuration"
	}
	if hasHTTPStatus(err, http.StatusForbidden) {
		raw403 := strings.ToLower(err.Error())
		if strings.Contains(raw403, "access_terminated") ||
			(strings.Contains(raw403, "usage limit") && strings.Contains(raw403, "billing cycle")) {
			if zh {
				return "API 额度已用完（本计费周期），将在下个周期刷新。如需立即使用，请购买额外额度或升级套餐"
			}
			return "API quota exhausted for this billing cycle. It will refresh next cycle. Purchase extra usage or upgrade your plan to continue now"
		}
		if strings.Contains(raw403, "rate limit") || strings.Contains(raw403, "quota") {
			if zh {
				return "请求太频繁或额度不足，请稍后重试或升级套餐"
			}
			return "Rate limited or quota exceeded. Please retry shortly or upgrade your plan"
		}
		if zh {
			return "无权访问该接口，请确认你的账号权限和 API Key"
		}
		return "Access denied. Please verify your account permissions and API key"
	}
	if hasHTTPStatus(err, http.StatusNotFound) {
		if zh {
			return "接口不存在 (404)，请检查 Base URL 和模型名称是否正确"
		}
		return "Endpoint not found (404). Please check your Base URL and model name"
	}

	// ---- Rate limiting (429) ----
	// Many coding plan providers (ZAI/GLM, OpenAI, Anthropic) return 429 for
	// both transient rate limits AND permanent quota exhaustion. We must
	// distinguish them to give the user actionable guidance.
	if hasHTTPStatus(err, http.StatusTooManyRequests) {
		l429 := strings.ToLower(err.Error())
		if strings.Contains(l429, "coding plan") ||
			strings.Contains(l429, "usage limit") ||
			strings.Contains(l429, "使用上限") ||
			strings.Contains(l429, "套餐已到期") ||
			strings.Contains(l429, "package has expired") ||
			strings.Contains(l429, "insufficient balance") ||
			strings.Contains(l429, "余额不足") ||
			strings.Contains(l429, "欠费") ||
			strings.Contains(l429, "quota exceeded") ||
			strings.Contains(l429, "exceeded your current quota") ||
			strings.Contains(l429, "额度已用完") ||
			strings.Contains(l429, "allocated quota") ||
			strings.Contains(l429, "公平使用") ||
			strings.Contains(l429, "fair usage") {
			if zh {
				return "API 额度已用完或套餐已过期。请前往服务商页面查看额度状态、续订或充值后重试"
			}
			return "API quota exhausted or plan expired. Check your provider dashboard, renew your plan or add credits, then retry"
		}
		// Transient rate limit — safe to retry immediately
		if zh {
			return "请求太频繁，服务端已限流。输入 /retry 重试"
		}
		return "Rate limited by the server. Type /retry to resend"
	}

	// ---- Server errors ----
	if hasHTTPStatus(err, http.StatusBadGateway) ||
		hasHTTPStatus(err, http.StatusServiceUnavailable) ||
		hasHTTPStatus(err, http.StatusGatewayTimeout) {
		if zh {
			return "服务暂时不可用，请稍后重试"
		}
		return "Service temporarily unavailable. Please retry shortly"
	}
	if code := extractHTTPStatus(err); code >= 500 {
		if zh {
			return fmt.Sprintf("服务端错误 (%d)，请稍后重试", code)
		}
		return fmt.Sprintf("Server error (%d). Please retry shortly", code)
	}

	// ---- Context / timeout errors ----
	if errors.Is(err, context.Canceled) || errors.Is(inner, context.Canceled) {
		if zh {
			return "请求已取消"
		}
		return "Request cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(inner, context.DeadlineExceeded) {
		if zh {
			return "请求超时。输入 /retry 重试"
		}
		return "Request timed out. Type /retry to resend"
	}

	// ---- Network errors ----
	var netErr net.Error
	if errors.As(err, &netErr) || errors.As(inner, &netErr) {
		if zh {
			return "网络连接失败，请检查网络设置后输入 /retry 重试"
		}
		return "Network connection failed. Check your network settings and type /retry"
	}

	// ---- Anthropic serialization error ----
	raw := err.Error()
	if isAnthropicSerializationError(raw) {
		if zh {
			return "发送请求失败：消息格式不兼容。请尝试 /compact 后重试"
		}
		return "Request failed: incompatible message format. Try /compact and retry"
	}

	// ---- Context window / prompt too long ----
	if strings.Contains(raw, "context_window") || strings.Contains(raw, "context length") ||
		strings.Contains(raw, "max_tokens") || strings.Contains(raw, "prompt too long") ||
		strings.Contains(raw, "token limit") {
		if zh {
			return "对话内容过长，已超出模型上下文限制。请尝试 /compact 或缩短对话"
		}
		return "Conversation too long, exceeds model context limit. Try /compact or start a new session"
	}

	// ---- 400 Bad Request ----
	if hasHTTPStatus(err, http.StatusBadRequest) {
		if zh {
			return "请求参数错误 (400)，请尝试 /compact 或重新开始对话"
		}
		return "Bad request (400). Try /compact or start a new session"
	}

	// ---- Generic HTTP errors ----
	if code := extractHTTPStatus(err); code > 0 {
		if zh {
			return fmt.Sprintf("请求失败 (%d)，请稍后重试", code)
		}
		return fmt.Sprintf("Request failed (%d). Please retry shortly", code)
	}

	// ---- Connection refused / DNS ----
	if strings.Contains(raw, "connection refused") || strings.Contains(raw, "no such host") {
		if zh {
			return "无法连接到 API 服务器，请检查网络和 Base URL 设置"
		}
		return "Cannot connect to the API server. Please check your network and Base URL"
	}

	// ---- Stream finish_reason / stop_reason errors ----
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "finish_reason=length") || strings.Contains(lower, "stop_reason=max_tokens") || strings.Contains(lower, "finish_reason=max_tokens") {
		if zh {
			return "回复被截断（超出最大输出长度）。可尝试增加 max_tokens 或缩短问题"
		}
		return "Response truncated (max output length reached). Try increasing max_tokens or shortening your prompt"
	}
	if strings.Contains(lower, "content_filter") || strings.Contains(lower, "stop_reason=refusal") || strings.Contains(lower, "finish_reason=sensitive") {
		if zh {
			return "回复被内容过滤器拦截"
		}
		return "Response blocked by content filter"
	}
	if strings.Contains(lower, "finish_reason=network_error") {
		if zh {
			return "网络错误导致流式传输中断，请输入 /retry 重试"
		}
		return "Network error interrupted the stream. Type /retry to resend"
	}
	if strings.Contains(lower, "context_window_exceeded") || strings.Contains(lower, "prompt too long: model context window exceeded") {
		if zh {
			return "对话内容过长，已超出模型上下文限制。请尝试 /compact 或缩短对话"
		}
		return "Conversation too long, exceeds model context limit. Try /compact or start a new session"
	}

	// ---- Quota / billing exhausted (string-based, for when error chain was destroyed) ----
	// Covers Kimi (access_terminated), ZAI/GLM (coding plan 1308-1321),
	// OpenAI (quota), and generic patterns.
	if strings.Contains(lower, "access_terminated") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "billing cycle") ||
		strings.Contains(lower, "quota exhausted") ||
		strings.Contains(lower, "使用上限") ||
		strings.Contains(lower, "套餐已到期") ||
		strings.Contains(lower, "package has expired") ||
		strings.Contains(lower, "insufficient balance") ||
		strings.Contains(lower, "余额不足") ||
		strings.Contains(lower, "欠费") ||
		strings.Contains(lower, "额度已用完") ||
		strings.Contains(lower, "allocated quota") ||
		strings.Contains(lower, "公平使用") ||
		strings.Contains(lower, "fair usage") ||
		strings.Contains(lower, "coding plan") {
		if zh {
			return "API 额度已用完或套餐已过期。请前往服务商页面查看额度状态、续订或充值后重试"
		}
		return "API quota exhausted or plan expired. Check your provider dashboard, renew your plan or add credits, then retry"
	}

	// ---- Fallback: generic message, strip provider noise ----
	msg := raw
	for _, prefix := range []string{
		"openai chat: ",
		"openai stream: ",
		"gemini chat: ",
		"gemini stream: ",
	} {
		if strings.HasPrefix(msg, prefix) {
			msg = strings.TrimPrefix(msg, prefix)
			break
		}
	}
	if msg != "" && msg != raw {
		if zh {
			return "请求失败：" + msg
		}
		return "Request failed: " + msg
	}

	if zh {
		return "请求失败，请稍后重试"
	}
	return "Request failed. Please retry shortly"
}

// hasHTTPStatus checks whether err (or any wrapped error) carries the given
// HTTP status code via known SDK error types.
func hasHTTPStatus(err error, status int) bool {
	if err == nil {
		return false
	}
	var openaiAPIErr *openai.APIError
	if errors.As(err, &openaiAPIErr) && openaiAPIErr.HTTPStatusCode == status {
		return true
	}
	var openaiReqErr *openai.RequestError
	if errors.As(err, &openaiReqErr) && openaiReqErr.HTTPStatusCode == status {
		return true
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) && anthropicErr.StatusCode == status {
		return true
	}
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == status {
		return true
	}
	return false
}

// extractHTTPStatus returns the HTTP status code embedded in err, or 0 if none found.
func extractHTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	var openaiAPIErr *openai.APIError
	if errors.As(err, &openaiAPIErr) {
		return openaiAPIErr.HTTPStatusCode
	}
	var openaiReqErr *openai.RequestError
	if errors.As(err, &openaiReqErr) {
		return openaiReqErr.HTTPStatusCode
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode
	}
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return httpErr.HTTPStatusCode()
	}
	return 0
}

func isAnthropicSerializationError(raw string) bool {
	if raw == "" {
		return false
	}
	return strings.Contains(raw, "MarshalJSON") &&
		(strings.Contains(raw, "anthropic.MessageParam") ||
			strings.Contains(raw, "anthropic.ContentBlockParamUnion"))
}
