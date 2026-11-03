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

const (
	maxRateLimitRetries = 2
	defaultRetryDelay   = 2 * time.Second
	maxRetryDelay       = 30 * time.Second
)

func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return defaultRetryDelay
	}

	if ra := resp.Header.Get("Retry-After"); ra != "" {
		ra = strings.TrimSpace(ra)

		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			return capDuration(time.Duration(secs) * time.Second)
		}

		if fsecs, err := strconv.ParseFloat(ra, 64); err == nil && fsecs >= 0 {
			return capDuration(time.Duration(fsecs * float64(time.Second)))
		}

		if t, err := http.ParseTime(ra); err == nil {
			d := time.Until(t)
			if d > 0 {
				return capDuration(d)
			}
		}
	}

	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if ms, err := strconv.ParseInt(strings.TrimSpace(reset), 10, 64); err == nil && ms > 0 {
			resetTime := time.Unix(0, ms*int64(time.Millisecond))
			d := time.Until(resetTime)
			if d > 0 {
				return capDuration(d)
			}
		}
	}

	if resetAfter := resp.Header.Get("X-RateLimit-Reset-After"); resetAfter != "" {
		if fsecs, err := strconv.ParseFloat(strings.TrimSpace(resetAfter), 64); err == nil && fsecs >= 0 {
			return capDuration(time.Duration(fsecs * float64(time.Second)))
		}
	}

	if reset := resp.Header.Get("x-ogw-ratelimit-reset"); reset != "" {
		if fsecs, err := strconv.ParseFloat(strings.TrimSpace(reset), 64); err == nil && fsecs >= 0 {
			return capDuration(time.Duration(fsecs * float64(time.Second)))
		}
	}

	return defaultRetryDelay
}

func capDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRetryDelay
	}
	if d > maxRetryDelay {
		return maxRetryDelay
	}
	return d
}

var sleepRetryFn = func(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sleepRetry(ctx context.Context, d time.Duration) error {
	return sleepRetryFn(ctx, d)
}

func jitterDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}

	multiplier := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(d) * multiplier)
}

func rateLimitExhausted(platform string) error {
	return fmt.Errorf("%s API rate limited: max retries (%d) exceeded", platform, maxRateLimitRetries)
}
