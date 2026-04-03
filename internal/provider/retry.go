package provider

import (
	"context"
	"errors"
	"strings"
	"time"
)

// isRetryable returns true for HTTP 429 (rate limit) and 5xx (server error).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// go-openai wraps errors as APIError with HTTPStatusCode
	// Anthropic SDK may return errors with HTTP status codes
	// Gemini SDK returns errors that may contain status info
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		status := httpErr.HTTPStatusCode()
		return status == httpStatusTooManyRequests || status >= 500
	}
	// Fallback: check error message for status codes
	msg := err.Error()
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate") {
		return true
	}
	if strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") {
		return true
	}
	return false
}

// retryWithBackoff retries fn up to maxRetries times with exponential backoff (1s, 2s, 4s).
// Only retries if the error is retryable (429 or 5xx).
func retryWithBackoff(fn func() error, maxRetries int) error {
	return retryWithBackoffCtx(context.Background(), fn, maxRetries)
}

// retryWithBackoffCtx is like retryWithBackoff but respects context cancellation.
func retryWithBackoffCtx(ctx context.Context, fn func() error, maxRetries int) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) || i == maxRetries-1 {
			return err
		}
		select {
		case <-time.After(time.Duration(1<<uint(i)) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}

const httpStatusTooManyRequests = 429
