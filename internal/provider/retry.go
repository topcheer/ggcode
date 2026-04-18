package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

const (
	providerRetryAttempts   = 10
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
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation/deadline is never retryable.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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

// dumpRequestJSON serializes v to JSON and writes it to a temp file for
// debugging protocol violations (e.g. malformed messages causing API 500s).
func dumpRequestJSON(provider, method string, v any) {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		debug.Log(provider, "%s request dump marshal FAILED: %v", method, err)
		return
	}
	debug.Log(provider, "%s request JSON len=%d", method, len(jsonBytes))
	dumpPath := filepath.Join(os.TempDir(), "ggcode-"+provider+"-last-request.json")
	if writeErr := os.WriteFile(dumpPath, jsonBytes, 0644); writeErr != nil {
		debug.Log(provider, "%s request dump write failed: %v", method, writeErr)
	}
}
