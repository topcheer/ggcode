package im

// Pin tests for parseRetryAfter behavioral seams (r206).
// These were written and verified GREEN against the UNREFACTORED
// implementation (golden filter discipline) and must stay green
// after behavior-preserving extraction of the header-parsing helpers.
// Existing coverage in rate_limit_test.go pins each header path in
// isolation; these pins lock the cross-header semantics that a split
// could accidentally break: source priority order, fallthrough on
// unparseable/negative/past values, and whitespace trimming.

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func newRateLimitResp() *http.Response {
	return &http.Response{Header: http.Header{}}
}

func unixMSIn(d time.Duration) string {
	return strconv.FormatInt(time.Now().Add(d).UnixMilli(), 10)
}

// Priority: Retry-After must win over every platform-specific header.
func TestParseRetryAfterPin_PriorityRetryAfterWins(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("Retry-After", "2")
	resp.Header.Set("X-RateLimit-Reset", unixMSIn(9*time.Second))
	resp.Header.Set("X-RateLimit-Reset-After", "9.5")
	resp.Header.Set("x-ogw-ratelimit-reset", "8.5")
	if d := parseRetryAfter(resp); d != 2*time.Second {
		t.Fatalf("expected 2s from Retry-After priority, got %v", d)
	}
}

// X-RateLimit-Reset outranks the fractional-seconds headers.
func TestParseRetryAfterPin_ResetOutranksResetAfter(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("X-RateLimit-Reset", unixMSIn(4*time.Second))
	resp.Header.Set("X-RateLimit-Reset-After", "9.5")
	resp.Header.Set("x-ogw-ratelimit-reset", "8.5")
	d := parseRetryAfter(resp)
	if d < 3*time.Second || d > 6*time.Second {
		t.Fatalf("expected ~4s from X-RateLimit-Reset priority, got %v", d)
	}
}

// Negative integer Retry-After must fall through to default (no panic,
// no negative duration, later headers still consulted).
func TestParseRetryAfterPin_NegativeRetryAfterFallsThrough(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("Retry-After", "-1")
	resp.Header.Set("X-RateLimit-Reset-After", "1.5")
	if d := parseRetryAfter(resp); d != 1500*time.Millisecond {
		t.Fatalf("expected fallthrough to Reset-After 1.5s, got %v", d)
	}
}

// Negative float Retry-After falls through the same way.
func TestParseRetryAfterPin_NegativeFloatRetryAfterFallsThrough(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("Retry-After", "-0.5")
	if d := parseRetryAfter(resp); d != defaultRetryDelay {
		t.Fatalf("expected default for negative float, got %v", d)
	}
}

// Padded values are trimmed before parsing (Retry-After and the
// fractional-seconds headers share this behavior).
func TestParseRetryAfterPin_PaddedValuesTrimmed(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("Retry-After", "  3  ")
	if d := parseRetryAfter(resp); d != 3*time.Second {
		t.Fatalf("expected 3s for padded Retry-After, got %v", d)
	}
	resp2 := newRateLimitResp()
	resp2.Header.Set("X-RateLimit-Reset-After", " 1.25 ")
	if d := parseRetryAfter(resp2); d != 1250*time.Millisecond {
		t.Fatalf("expected 1.25s for padded Reset-After, got %v", d)
	}
}

// Mattermost unix-ms: future timestamp yields time-until, capped path.
func TestParseRetryAfterPin_MattermostUnixMSFuture(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("X-RateLimit-Reset", unixMSIn(5*time.Second))
	d := parseRetryAfter(resp)
	if d < 3*time.Second || d > 7*time.Second {
		t.Fatalf("expected ~5s until unix-ms reset, got %v", d)
	}
}

// Mattermost unix-ms in the past must fall through to later headers.
func TestParseRetryAfterPin_MattermostUnixMSPastFallsThrough(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("X-RateLimit-Reset", unixMSIn(-10*time.Second))
	resp.Header.Set("x-ogw-ratelimit-reset", "1.5")
	if d := parseRetryAfter(resp); d != 1500*time.Millisecond {
		t.Fatalf("expected fallthrough to ogw 1.5s, got %v", d)
	}
}

// Garbage unix-ms must fall through to default, not zero.
func TestParseRetryAfterPin_ResetGarbageFallsToDefault(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("X-RateLimit-Reset", "abc")
	if d := parseRetryAfter(resp); d != defaultRetryDelay {
		t.Fatalf("expected default for garbage Reset, got %v", d)
	}
}

// HTTP-date in the past falls through to later headers.
func TestParseRetryAfterPin_HTTPDatePastFallsThrough(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("Retry-After", time.Now().Add(-1*time.Hour).UTC().Format(http.TimeFormat))
	resp.Header.Set("X-RateLimit-Reset-After", "1.25")
	if d := parseRetryAfter(resp); d != 1250*time.Millisecond {
		t.Fatalf("expected fallthrough to Reset-After 1.25s, got %v", d)
	}
}

// Unparseable Retry-After must still consult platform headers.
func TestParseRetryAfterPin_UnparseableRetryAfterUsesResetAfter(t *testing.T) {
	resp := newRateLimitResp()
	resp.Header.Set("Retry-After", "not-a-number")
	resp.Header.Set("X-RateLimit-Reset-After", "2.5")
	if d := parseRetryAfter(resp); d != 2500*time.Millisecond {
		t.Fatalf("expected fallthrough to Reset-After 2.5s, got %v", d)
	}
}

// --- seam-level unit tests (added with the r206 extraction; additive) ---

func TestRetryAfterHeaderValue_Forms(t *testing.T) {
	if d, ok := retryAfterHeaderValue("7"); !ok || d != 7*time.Second {
		t.Fatalf("int: expected 7s ok, got %v %v", d, ok)
	}
	if d, ok := retryAfterHeaderValue("0"); !ok || d != 0 {
		t.Fatalf("zero: expected 0 ok, got %v %v", d, ok)
	}
	if d, ok := retryAfterHeaderValue(" 0.25 "); !ok || d != 250*time.Millisecond {
		t.Fatalf("float: expected 250ms ok, got %v %v", d, ok)
	}
	if d, ok := retryAfterHeaderValue("-3"); ok {
		t.Fatalf("negative int: expected !ok, got %v", d)
	}
	if d, ok := retryAfterHeaderValue("-0.5"); ok {
		t.Fatalf("negative float: expected !ok, got %v", d)
	}
	if _, ok := retryAfterHeaderValue("garbage"); ok {
		t.Fatal("garbage: expected !ok")
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if d, ok := retryAfterHeaderValue(future); !ok || d <= 0 {
		t.Fatalf("future date: expected ok positive, got %v %v", d, ok)
	}
	past := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
	if d, ok := retryAfterHeaderValue(past); ok {
		t.Fatalf("past date: expected !ok, got %v", d)
	}
}

func TestRetryAfterFromUnixMilli_Forms(t *testing.T) {
	if d, ok := retryAfterFromUnixMilli(unixMSIn(5 * time.Second)); !ok || d <= 0 {
		t.Fatalf("future: expected ok positive, got %v %v", d, ok)
	}
	if d, ok := retryAfterFromUnixMilli(unixMSIn(-5 * time.Second)); ok {
		t.Fatalf("past: expected !ok, got %v", d)
	}
	if d, ok := retryAfterFromUnixMilli("0"); ok {
		t.Fatalf("zero: expected !ok, got %v", d)
	}
	if d, ok := retryAfterFromUnixMilli("-100"); ok {
		t.Fatalf("negative: expected !ok, got %v", d)
	}
	if d, ok := retryAfterFromUnixMilli("junk"); ok {
		t.Fatalf("junk: expected !ok, got %v", d)
	}
}

func TestFractionalSecondsDuration_Forms(t *testing.T) {
	if d, ok := fractionalSecondsDuration("2.5"); !ok || d != 2500*time.Millisecond {
		t.Fatalf("valid: expected 2.5s ok, got %v %v", d, ok)
	}
	if d, ok := fractionalSecondsDuration(" 1.25 "); !ok || d != 1250*time.Millisecond {
		t.Fatalf("padded: expected 1.25s ok, got %v %v", d, ok)
	}
	if d, ok := fractionalSecondsDuration("-1"); ok {
		t.Fatalf("negative: expected !ok, got %v", d)
	}
	if d, ok := fractionalSecondsDuration("abc"); ok {
		t.Fatalf("garbage: expected !ok, got %v", d)
	}
}
