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
// human-readable message suitable for display in the TUI or IM.
//
// It strips SDK-specific noise, maps HTTP status codes to friendly text, and
// falls back to a generic message so users never see raw stack traces or
// protocol details.
func UserFacingError(err error) string {
	if err == nil {
		return ""
	}

	// Unwrap once to handle our own fmt.Errorf("xxx: %w", err) wrappers.
	inner := errors.Unwrap(err)
	if inner == nil {
		inner = err
	}

	// ---- Auth / permission errors (never retried) ----
	if hasHTTPStatus(err, http.StatusUnauthorized) {
		return "API 密钥无效或未配置，请检查你的 API Key 设置"
	}
	if hasHTTPStatus(err, http.StatusForbidden) {
		return "无权访问该接口，请确认你的账号权限和 API Key"
	}
	if hasHTTPStatus(err, http.StatusNotFound) {
		return "接口不存在 (404)，请检查 Base URL 和模型名称是否正确"
	}

	// ---- Rate limiting ----
	if hasHTTPStatus(err, http.StatusTooManyRequests) {
		return "请求太频繁，服务端已限流，请稍后重试"
	}

	// ---- Server errors ----
	if hasHTTPStatus(err, http.StatusBadGateway) ||
		hasHTTPStatus(err, http.StatusServiceUnavailable) ||
		hasHTTPStatus(err, http.StatusGatewayTimeout) {
		return "服务暂时不可用，请稍后重试"
	}
	if code := extractHTTPStatus(err); code >= 500 {
		return fmt.Sprintf("服务端错误 (%d)，请稍后重试", code)
	}

	// ---- Context / timeout errors ----
	if errors.Is(err, context.Canceled) || errors.Is(inner, context.Canceled) {
		return "请求已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(inner, context.DeadlineExceeded) {
		return "请求超时，请稍后重试"
	}

	// ---- Network errors ----
	var netErr net.Error
	if errors.As(err, &netErr) || errors.As(inner, &netErr) {
		return "网络连接失败，请检查网络设置后重试"
	}

	// ---- Anthropic serialization error ----
	raw := err.Error()
	if isAnthropicSerializationError(raw) {
		return "发送请求失败：消息格式不兼容。请尝试 /compact 后重试"
	}

	// ---- Context window / prompt too long ----
	if strings.Contains(raw, "context_window") || strings.Contains(raw, "context length") ||
		strings.Contains(raw, "max_tokens") || strings.Contains(raw, "prompt too long") ||
		strings.Contains(raw, "token limit") {
		return "对话内容过长，已超出模型上下文限制。请尝试 /compact 或缩短对话"
	}

	// ---- 400 Bad Request ----
	if hasHTTPStatus(err, http.StatusBadRequest) {
		return "请求参数错误 (400)，请尝试 /compact 或重新开始对话"
	}

	// ---- Generic HTTP errors ----
	if code := extractHTTPStatus(err); code > 0 {
		return fmt.Sprintf("请求失败 (%d)，请稍后重试", code)
	}

	// ---- Connection refused / DNS ----
	if strings.Contains(raw, "connection refused") || strings.Contains(raw, "no such host") {
		return "无法连接到 API 服务器，请检查网络和 Base URL 设置"
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
		return "请求失败：" + msg
	}

	return "请求失败，请稍后重试"
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
