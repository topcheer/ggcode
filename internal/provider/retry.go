package provider

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

const (
	httpStatusTooManyRequests = 429
	providerRetryAttempts     = 10
	providerRetryBackoffCap   = 30 * time.Second
)

var retrySleep = func(ctx context.Context, delay time.Duration) error {
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isRetryable returns true for HTTP 429 (rate limit) and 5xx (server error).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
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
	// go-openai wraps errors as APIError with HTTPStatusCode
	// Anthropic SDK may return errors with HTTP status codes
	// Gemini SDK returns errors that may contain status info
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return isRetryableHTTPStatus(httpErr.HTTPStatusCode())
	}
	// Fallback: check error message for status codes
	msg := err.Error()
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate") {
		return true
	}
	if strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") {
		return true
	}
	// ZAI platform transient errors (e.g. "网络错误")
	if strings.Contains(msg, "网络错误") {
		return true
	}
	return false
}

func isRetryableHTTPStatus(status int) bool {
	return status == httpStatusTooManyRequests || status >= 500
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
		if !isRetryable(err) || i == maxAttempts-1 {
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
