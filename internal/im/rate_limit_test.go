package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- parseRetryAfter unit tests ---

func TestParseRetryAfter_IntegerSeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "5")
	d := parseRetryAfter(resp)
	if d != 5*time.Second {
		t.Fatalf("expected 5s, got %v", d)
	}
}

func TestParseRetryAfter_ZeroSeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "0")
	d := parseRetryAfter(resp)
	if d != defaultRetryDelay {
		t.Fatalf("expected default %v for 0s, got %v", defaultRetryDelay, d)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	future := time.Now().Add(10 * time.Second)
	resp.Header.Set("Retry-After", future.UTC().Format(http.TimeFormat))
	d := parseRetryAfter(resp)
	if d <= 0 || d > 15*time.Second {
		t.Fatalf("expected ~10s, got %v", d)
	}
}

func TestParseRetryAfter_MissingHeader(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	d := parseRetryAfter(resp)
	if d != defaultRetryDelay {
		t.Fatalf("expected default %v, got %v", defaultRetryDelay, d)
	}
}

func TestParseRetryAfter_Garbage(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "not-a-number")
	d := parseRetryAfter(resp)
	if d != defaultRetryDelay {
		t.Fatalf("expected default for garbage, got %v", d)
	}
}

func TestParseRetryAfter_MattermostXRateLimitReset(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	// X-RateLimit-Reset is Unix timestamp in milliseconds
	futureMs := time.Now().Add(3 * time.Second).UnixMilli()
	resp.Header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", futureMs))
	d := parseRetryAfter(resp)
	if d <= 0 || d > 10*time.Second {
		t.Fatalf("expected ~3s, got %v", d)
	}
}

func TestParseRetryAfter_NilResponse(t *testing.T) {
	d := parseRetryAfter(nil)
	if d != defaultRetryDelay {
		t.Fatalf("expected default for nil resp, got %v", d)
	}
}

func TestParseRetryAfter_CapsAtMax(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "999999")
	d := parseRetryAfter(resp)
	if d != maxRetryDelay {
		t.Fatalf("expected max %v, got %v", maxRetryDelay, d)
	}
}

func TestParseRetryAfter_FloatSeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "0.5")
	d := parseRetryAfter(resp)
	if d != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %v", d)
	}
}

func TestParseRetryAfter_FloatSecondsLarge(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "5.75")
	d := parseRetryAfter(resp)
	expected := 5*time.Second + 750*time.Millisecond
	if d != expected {
		t.Fatalf("expected %v, got %v", expected, d)
	}
}

func TestParseRetryAfter_DiscordXRateLimitResetAfter(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("X-RateLimit-Reset-After", "2.5")
	d := parseRetryAfter(resp)
	if d != 2500*time.Millisecond {
		t.Fatalf("expected 2500ms, got %v", d)
	}
}

func TestParseRetryAfter_FeishuFloatReset(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("x-ogw-ratelimit-reset", "1.5")
	d := parseRetryAfter(resp)
	if d != 1500*time.Millisecond {
		t.Fatalf("expected 1500ms, got %v", d)
	}
}

// --- capDuration unit tests ---

func TestCapDuration_NegativeReturnsDefault(t *testing.T) {
	d := capDuration(-5 * time.Second)
	if d != defaultRetryDelay {
		t.Fatalf("expected default for negative, got %v", d)
	}
}

func TestCapDuration_OverMaxCapped(t *testing.T) {
	d := capDuration(120 * time.Second)
	if d != maxRetryDelay {
		t.Fatalf("expected max %v, got %v", maxRetryDelay, d)
	}
}

func TestCapDuration_InRange(t *testing.T) {
	d := capDuration(5 * time.Second)
	if d != 5*time.Second {
		t.Fatalf("expected 5s, got %v", d)
	}
}

// --- sleepRetry unit tests ---

func TestSleepRetry_NormalCompletion(t *testing.T) {
	start := time.Now()
	err := sleepRetry(context.Background(), 50*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("sleep was too short: %v", elapsed)
	}
}

func TestSleepRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := sleepRetry(ctx, 5*time.Second)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// --- Integration: HTTP 429 retry behavior ---

// TestRetryOn429_SlackResponse simulates a Slack-style 429 then 200.
func TestRetryOn429_SlackResponse(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 2 {
			w.Header().Set("Retry-After", "0") // immediate retry for test speed
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"ts": "1234567890.000100",
		})
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		name:       "test",
		httpClient: srv.Client(),
		apiBase:    srv.URL,
		botToken:   "x",
	}

	ts, err := adapter.sendChannelMessage(context.Background(), "C123", "", "hello")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if ts != "1234567890.000100" {
		t.Fatalf("expected ts, got %q", ts)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls (2x429 + 1x200), got %d", callCount)
	}
}

// TestRetryOn429_SlackExhausted simulates Slack always returning 429.
func TestRetryOn429_SlackExhausted(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		name:       "test",
		httpClient: srv.Client(),
		apiBase:    srv.URL,
		botToken:   "x",
	}

	_, err := adapter.sendChannelMessage(context.Background(), "C123", "", "hello")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected 'rate limited' error, got: %v", err)
	}
	// maxRateLimitRetries + 1 initial = total calls
	expectedCalls := maxRateLimitRetries + 1
	if callCount != expectedCalls {
		t.Fatalf("expected %d calls, got %d", expectedCalls, callCount)
	}
}

// TestRetryOn429_DiscordResponse simulates Discord 429 then 200.
func TestRetryOn429_DiscordResponse(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent) // 204 success for Discord
	}))
	defer srv.Close()

	adapter := &discordAdapter{
		name:       "test",
		httpClient: srv.Client(),
		token:      "x",
		apiBase:    srv.URL,
	}

	err := adapter.sendChannelMessage(context.Background(), "123", "hello")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (1x429 + 1x204), got %d", callCount)
	}
}

// TestRetryOn429_MattermostResponse simulates Mattermost 429 then 200.
func TestRetryOn429_MattermostResponse(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// Mattermost uses X-RateLimit-Reset (ms timestamp)
			futureMs := time.Now().Add(1 * time.Millisecond).UnixMilli()
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", futureMs))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "post123",
		})
	}))
	defer srv.Close()

	adapter := &mattermostAdapter{
		name:    "test",
		baseURL: srv.URL,
		token:   "x",
		conn:    srv.Client(),
	}

	result, err := adapter.apiPostCtx(context.Background(), "posts", map[string]any{
		"channel_id": "ch1",
		"message":    "hello",
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if result["id"] != "post123" {
		t.Fatalf("expected post123, got %v", result["id"])
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
}

// TestNoRetryOn429_DiscordExhausted verifies error message after max retries.
func TestNoRetryOn429_DiscordExhausted(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	adapter := &discordAdapter{
		name:       "test",
		httpClient: srv.Client(),
		token:      "x",
		apiBase:    srv.URL,
	}

	err := adapter.sendChannelMessage(context.Background(), "123", "hello")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected 'rate limited' in error, got: %v", err)
	}
}

// TestSlackRatelimitedInBody tests Slack's application-level ratelimited error.
func TestSlackRatelimitedInBody(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// Slack can return 200 with ok=false and error=ratelimited
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "ratelimited",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"ts": "999",
		})
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		name:       "test",
		httpClient: srv.Client(),
		apiBase:    srv.URL,
		botToken:   "x",
	}

	ts, err := adapter.sendChannelMessage(context.Background(), "C123", "", "hello")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if ts != "999" {
		t.Fatalf("expected ts=999, got %q", ts)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 calls (1x ratelimited + 1x ok), got %d", callCount)
	}
}
