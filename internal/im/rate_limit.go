package im

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Rate limit constants.
// Sources:
//   - Slack: HTTP 429 with Retry-After header (seconds)
//     https://api.slack.com/apis/rate-limits
//   - Discord: HTTP 429 with Retry-After header (float seconds) or JSON body {"retry_after": N}
//     https://discord.com/developers/docs/topics/rate-limits
//   - Mattermost: HTTP 429 with X-RateLimit-Reset header (Unix ms timestamp) or Retry-After
//     https://docs.mattermost.com/administration-guide/manage/rate-limit-settings.html
const (
	// maxRateLimitRetries is the maximum number of retry attempts after a 429.
	maxRateLimitRetries = 2
	// defaultRetryDelay is used when no Retry-After header is present.
	defaultRetryDelay = 2 * time.Second
	// maxRetryDelay caps any single retry wait to prevent excessive blocking.
	maxRetryDelay = 30 * time.Second
)

// parseRetryAfter extracts the retry delay from a 429 response.
// It checks the standard Retry-After header first, then falls back to
// platform-specific headers (X-RateLimit-Reset for Mattermost).
// Returns defaultRetryDelay if no header is present or parseable.
func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return defaultRetryDelay
	}

	// Standard Retry-After header (seconds or HTTP-date).
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		ra = strings.TrimSpace(ra)
		// Try integer seconds first.
		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			return capDuration(time.Duration(secs) * time.Second)
		}
		// Try float seconds (e.g. Discord sends "0.5"; some APIs use fractional values).
		if fsecs, err := strconv.ParseFloat(ra, 64); err == nil && fsecs >= 0 {
			return capDuration(time.Duration(fsecs * float64(time.Second)))
		}
		// Try HTTP-date format.
		if t, err := http.ParseTime(ra); err == nil {
			d := time.Until(t)
			if d > 0 {
				return capDuration(d)
			}
		}
	}

	// Mattermost uses X-RateLimit-Reset (Unix timestamp in milliseconds).
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if ms, err := strconv.ParseInt(strings.TrimSpace(reset), 10, 64); err == nil && ms > 0 {
			resetTime := time.Unix(0, ms*int64(time.Millisecond))
			d := time.Until(resetTime)
			if d > 0 {
				return capDuration(d)
			}
		}
	}

	// Discord uses X-RateLimit-Reset-After (seconds until reset, can be fractional).
	// https://discord.com/developers/docs/topics/rate-limits
	if resetAfter := resp.Header.Get("X-RateLimit-Reset-After"); resetAfter != "" {
		if fsecs, err := strconv.ParseFloat(strings.TrimSpace(resetAfter), 64); err == nil && fsecs >= 0 {
			return capDuration(time.Duration(fsecs * float64(time.Second)))
		}
	}

	// Feishu uses x-ogw-ratelimit-reset (seconds until reset, may be fractional).
	// https://open.feishu.cn/document/server-docs/api-call-guide/frequency-control
	if reset := resp.Header.Get("x-ogw-ratelimit-reset"); reset != "" {
		if fsecs, err := strconv.ParseFloat(strings.TrimSpace(reset), 64); err == nil && fsecs >= 0 {
			return capDuration(time.Duration(fsecs * float64(time.Second)))
		}
	}

	return defaultRetryDelay
}

// capDuration clamps a retry delay to maxRetryDelay.
func capDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRetryDelay
	}
	if d > maxRetryDelay {
		return maxRetryDelay
	}
	return d
}

// sleepRetry sleeps for the given duration, respecting context cancellation.
// Returns ctx.Err() if the context is cancelled during the sleep.
func sleepRetry(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// jitterDuration applies ±25% random jitter to a duration to prevent the
// thundering herd problem where multiple adapters reconnect at identical
// intervals. For example, a 10s backoff becomes a random value in [7.5s, 12.5s).
func jitterDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	// Multiplier in [0.75, 1.25).
	multiplier := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(d) * multiplier)
}

// rateLimitExhausted formats a standard error for when all retries are used up.
func rateLimitExhausted(platform string) error {
	return fmt.Errorf("%s API rate limited: max retries (%d) exceeded", platform, maxRateLimitRetries)
}
