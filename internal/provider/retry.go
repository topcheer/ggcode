package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

const (
	providerRetryAttempts   = 20
	providerRetryBackoffCap = 30 * time.Second
)

var retrySleep = func(ctx context.Context, delay time.Duration) error {
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isRetryable returns true for any error that is worth retrying.
//
// We retry aggressively: only 401 (auth), 403 (forbidden), and 404 (not found)
// are considered permanent failures. Everything else — rate limits, server
// errors, timeouts, network glitches, bad gateway, etc. — gets retried.
// IsContextOverflowError checks whether the error indicates the input prompt
// exceeds the model's context window. These errors are never retryable — the
// same request will always fail until the context is compacted.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	// Guard against SDK error types whose .Error() panics on nil internals
	// (e.g., anthropic.Error with nil Response).
	msg := func() string {
		defer func() { recover() }()
		return err.Error()
	}()
	s := strings.ToLower(msg)
	keywords := []string{
		"context_length_exceeded",
		"maximum context",
		"context length",
		"prompt is too long",
		"prompt too long",
		"prompt exceeds",
		"max length",
		"超长",
		"exceeds the maximum",
		"request too large",
		"too many tokens",
		"input is too long",
		"exceeds the model's context",
		"token limit",
		"exceeds the limit",
		"token count exceeds",
		"input is too long for",
		"input length exceeds",
		"prompt tokens too long",
		"prompt tokens exceeds",
		"must have less than",
		"range of input length",
		"超出了模型最大",
		"token限制",
		"maximum input tokens",
		"input tokens exceeded",
		"context window",
	}
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// User/session cancellation is never retryable. DeadlineExceeded is handled
	// below as a retryable timeout unless the caller context has already ended.
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Prompt-too-long errors are permanent: retrying the same oversized request
	// will never succeed. Let the agent layer handle reactive compaction instead.
	if IsContextOverflowError(err) {
		return false
	}

	// Check for HTTP status codes from known SDK error types.
	var openaiAPIErr *openai.APIError
	if errors.As(err, &openaiAPIErr) {
		return isRetryableHTTPStatus(openaiAPIErr.HTTPStatusCode)
	}
	var openaiReqErr *openai.RequestError
	if errors.As(err, &openaiReqErr) {
		return isRetryableHTTPStatus(openaiReqErr.HTTPStatusCode)
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return isRetryableHTTPStatus(anthropicErr.StatusCode)
	}
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return isRetryableHTTPStatus(httpErr.HTTPStatusCode())
	}

	// Network / timeout errors are always retryable.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}

	// Fallback: check error message for known non-retryable status codes.
	msg := err.Error()
	if strings.Contains(msg, " 401 ") || strings.Contains(msg, "status\":401") || strings.Contains(msg, "statusCode:401") {
		return false
	}
	if strings.Contains(msg, " 403 ") || strings.Contains(msg, "status\":403") || strings.Contains(msg, "statusCode:403") {
		return false
	}
	if strings.Contains(msg, " 404 ") || strings.Contains(msg, "status\":404") || strings.Contains(msg, "statusCode:404") {
		return false
	}

	// Any other error with a recognizable HTTP status code is retryable.
	for _, code := range []string{
		"400", "408", "409", "422", "429",
		"500", "502", "503", "504", "520", "521", "522", "523", "524",
	} {
		if strings.Contains(msg, code) {
			return true
		}
	}

	// ZAI platform transient errors.
	if strings.Contains(msg, "网络错误") {
		return true
	}

	// Default: retry unknown errors. It's better to retry once too many
	// than to fail permanently on a transient issue.
	return true
}

func isRetryableForContext(ctx context.Context, err error) bool {
	// User cancellation is never retryable.
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	// DeadlineExceeded from the caller context means the agent turn
	// timed out — not retryable. But DeadlineExceeded from an HTTP
	// client timeout (where ctx is still alive) IS retryable.
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false
	}
	return isRetryable(err)
}

// isRetryableHTTPStatus returns true unless the status code is a permanent
// client error (401, 403, 404). All other codes — including 429, 5xx, and
// unexpected 4xx — are retried.
func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return false
	default:
		return true
	}
}

// retryWithBackoff retries fn up to maxAttempts times with exponential backoff.
// Only retries retryable errors (429 or 5xx), and honors Retry-After where available.
func retryWithBackoff(fn func() error, maxAttempts int) error {
	return retryWithBackoffCtx(context.Background(), fn, maxAttempts)
}

// retryWithBackoffCtx is like retryWithBackoff but respects context cancellation.
func retryWithBackoffCtx(ctx context.Context, fn func() error, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableForContext(ctx, err) || i == maxAttempts-1 {
			return err
		}
		if sleepErr := retrySleep(ctx, retryDelay(err, i)); sleepErr != nil {
			return sleepErr
		}
	}
	return lastErr
}

func retryDelay(err error, attempt int) time.Duration {
	if delay, ok := retryAfterDelay(err); ok && delay > 0 {
		return delay
	}
	if attempt < 0 {
		attempt = 0
	}
	delay := time.Second * time.Duration(1<<minInt(attempt, 5))
	if delay > providerRetryBackoffCap {
		return providerRetryBackoffCap
	}
	return delay
}

func retryAfterDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) && anthropicErr.Response != nil {
		return parseRetryAfterHeader(anthropicErr.Response.Header)
	}
	var responseErr interface{ Response() *http.Response }
	if errors.As(err, &responseErr) {
		if resp := responseErr.Response(); resp != nil {
			return parseRetryAfterHeader(resp.Header)
		}
	}
	return 0, false
}

func parseRetryAfterHeader(header http.Header) (time.Duration, bool) {
	if header == nil {
		return 0, false
	}
	retries := []struct {
		key    string
		units  time.Duration
		custom func(string) (time.Duration, bool)
	}{
		{
			key:   "Retry-After-Ms",
			units: time.Millisecond,
			custom: func(string) (time.Duration, bool) {
				return 0, false
			},
		},
		{
			key:   "Retry-After",
			units: time.Second,
			custom: func(v string) (time.Duration, bool) {
				t, err := time.Parse(time.RFC1123, v)
				if err != nil {
					return 0, false
				}
				return time.Until(t), true
			},
		},
	}
	for _, retry := range retries {
		value := header.Get(retry.key)
		if value == "" {
			continue
		}
		if retryAfter, err := strconv.ParseFloat(value, 64); err == nil {
			delay := time.Duration(retryAfter * float64(retry.units))
			if delay > 0 {
				return delay, true
			}
			return 0, false
		}
		if delay, ok := retry.custom(value); ok && delay > 0 {
			return delay, true
		}
	}
	return 0, false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// FriendlyError translates a raw provider error into a human-readable message
// with actionable advice. Returns the original error message if no pattern matches.
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	// Extract HTTP status code if available
	statusCode := 0
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		statusCode = anthropicErr.StatusCode
	}
	if statusCode == 0 {
		var openaiErr *openai.APIError
		if errors.As(err, &openaiErr) {
			statusCode = openaiErr.HTTPStatusCode
		}
	}
	if statusCode == 0 {
		var openaiReqErr *openai.RequestError
		if errors.As(err, &openaiReqErr) {
			statusCode = openaiReqErr.HTTPStatusCode
		}
	}
	if statusCode == 0 {
		for _, code := range []int{400, 401, 402, 403, 404, 408, 413, 422, 429, 500, 502, 503, 504} {
			if strings.Contains(msg, strconv.Itoa(code)) {
				statusCode = code
				break
			}
		}
	}

	// Context overflow — special handling
	if IsContextOverflowError(err) {
		return "The conversation has exceeded the model's context window. " +
			"Run /compact to compress the conversation history, or start a new session with /clear."
	}

	switch statusCode {
	case 401:
		return "Authentication failed (401). Your API key is invalid or expired. " +
			"Check your API key with: config set api_key=<your-key> " +
			"or verify the key in your provider dashboard."
	case 402:
		return "Payment required (402). Your API account has insufficient credits or billing. " +
			"Add credits or update billing in your provider dashboard."
	case 403:
		if strings.Contains(lower, "rate limit") || strings.Contains(lower, "quota") {
			return "Rate limit exceeded (403). You've hit your API quota. " +
				"Wait a moment and try again, or upgrade your plan for higher limits."
		}
		return "Access forbidden (403). Your API key may not have permission for this model, " +
			"or your account may be suspended. Check your provider dashboard."
	case 404:
		return "Model not found (404). The configured model may be deprecated or misspelled. " +
			"Check available models with: /model"
	case 408:
		return "Request timed out (408). The provider took too long to respond. " +
			"This is usually temporary — try sending your message again."
	case 413:
		return "Request too large (413). The message payload exceeds the server's limit. " +
			"Run /compact to reduce conversation size, or simplify your request."
	case 422:
		return "Request rejected (422). The provider couldn't process the request format. " +
			"This may be due to an unsupported feature (e.g., tool calling) for this model. " +
			"Try a different model or simplify your request."
	case 429:
		return "Rate limited (429). Too many requests in a short period. " +
			"Wait a few seconds and try again. Consider using a model with higher rate limits."
	case 500, 502, 503, 504:
		return fmt.Sprintf("Server error (%d). The provider is experiencing issues. "+
			"This is temporary — please retry in a moment.", statusCode)
	}

	// Check for common network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "Connection timed out. The provider server didn't respond in time. " +
				"Check your internet connection and try again."
		}
		return "Network error: unable to reach the provider server. " +
			"Check your internet connection and try again."
	}
	if errors.Is(err, io.EOF) {
		return "Connection closed unexpectedly by the provider. " +
			"This is usually temporary — try again."
	}

	// Cancellation
	if errors.Is(err, context.Canceled) {
		return "Request cancelled."
	}

	// Fallback: return original error
	return msg
}
