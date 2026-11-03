package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestIssue602_R1_StringFallback402NotRetryable(t *testing.T) {

	msgs := []string{
		`error, status code: 402, message: Payment Required - billing hard limit reached`,
		`Request failed with status code 402: credit balance is zero`,
		"400 Bad Request-ish decoy must not save us: status code: 402.",
	}
	for _, msg := range msgs {
		err := errors.New(msg)
		if !containsHTTPStatus(msg, "402") {
			t.Fatalf("containsHTTPStatus should recognize 402 in %q", msg)
		}
		if isRetryable(err) {
			t.Fatalf("isRetryable(string 402) = true, want false: %q", msg)
		}
	}
}

func TestIssue602_R1_StringFallback413NotRetryable(t *testing.T) {
	err := errors.New(`error, status code: 413, message: payload too large (request body exceeds limit)`)
	if isRetryable(err) {
		t.Fatal("isRetryable(string 413) = true, want false")
	}
}

func TestIssue602_R1_TypedVsStringAgreement(t *testing.T) {

	for _, code := range []int{400, 401, 402, 403, 404, 413, 422} {
		if isRetryableHTTPStatus(code) {
			t.Errorf("typed %d: isRetryableHTTPStatus = true, want false", code)
		}
		strErr := fmt.Errorf("error, status code: %d, message: boom", code)
		if isRetryable(strErr) {
			t.Errorf("string %d: isRetryable = true, want false", code)
		}
	}

	for _, code := range []int{408, 429, 500, 502, 503, 529} {
		if !isRetryableHTTPStatus(code) {
			t.Errorf("typed %d: isRetryableHTTPStatus = false, want true", code)
		}
		strErr := fmt.Errorf("error, status code: %d, message: boom", code)
		if !isRetryable(strErr) {
			t.Errorf("string %d: isRetryable = false, want true", code)
		}
	}
}

// mockStreamProvider yields a stream that emits N events and then blocks
// forever (never closes) — emulating a provider stuck mid-stream.
type mockStreamProvider struct {
	name   string
	events int
}

func (m *mockStreamProvider) Name() string { return m.name }
func (m *mockStreamProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *mockStreamProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, m.events)
	for i := 0; i < m.events; i++ {
		ch <- StreamEvent{Type: StreamEventText, Text: "x"}
	}

	return ch, nil
}
func (m *mockStreamProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 10, nil
}

func TestIssue602_R2_CancelledConsumerDoesNotLeakWrapper(t *testing.T) {
	primary := &mockStreamProvider{name: "primary", events: 3}
	fallback := &mockStreamProvider{name: "fallback", events: 3}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := fp.ChatStream(ctx, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	<-stream
	cancel()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stream channel not closed within 3s of ctx cancellation — wrapper goroutine leaked (R2)")
		}
	}
}

func TestIssue602_R2_FailoverDrainSurvivesCancellation(t *testing.T) {

	primary := &errThenHangProvider{name: "primary"}
	fallback := &mockStreamProvider{name: "fallback", events: 1}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := fp.ChatStream(ctx, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	gotSystem := false
	for !gotSystem {
		select {
		case ev, ok := <-stream:
			if !ok {
				t.Fatal("stream closed before failover system event")
			}
			if ev.Type == StreamEventSystem {
				gotSystem = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no failover system event within 3s")
		}
	}
	cancel()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("failover stream not closed within 3s of cancellation — wrapper/drain leaked (R2)")
		}
	}
}

// errThenHangProvider sends one error event and never closes the channel.
type errThenHangProvider struct {
	name string
}

func (m *errThenHangProvider) Name() string { return m.name }
func (m *errThenHangProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	return nil, errors.New("always fails")
}
func (m *errThenHangProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)

	ch <- StreamEvent{Type: StreamEventError, Error: errors.New("insufficient_quota: usage limit exceeded, upgrade your plan")}
	return ch, nil
}
func (m *errThenHangProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 10, nil
}

func TestIssue602_R3_FiveDigitStatusDoesNotPierceAuthAnchor(t *testing.T) {
	err := errors.New(`request failed: {"error":{"message":"invalid model id","type":"invalid_request_error","param":null,"code":"model_not_found","status":40139}}`)
	if got := ClassifyLLMError(err); got != FailureTransient {
		t.Fatalf("ClassifyLLMError(status=40139) = %v, want FailureTransient (not auth)", got)
	}
}

func TestIssue602_R3_FiveDigitStatusDoesNotPierceRateLimitAnchor(t *testing.T) {

	err := errors.New(`upstream responded {"error":{"message":"model unavailable for account tier","status":42999}}`)
	if got := ClassifyLLMError(err); got != FailureTransient {
		t.Fatalf("ClassifyLLMError(status=42999) = %v, want FailureTransient (not rate limit)", got)
	}
}

func TestIssue602_R3_RealFourDigitStatusStillMatches(t *testing.T) {

	authErr := errors.New(`error, status code: 401, message: invalid x-api-key`)
	if got := ClassifyLLMError(authErr); got != FailureAuth {
		t.Fatalf("ClassifyLLMError(401 with comma) = %v, want FailureAuth", got)
	}
	rlErr := errors.New(`error, status code: 429, message: rate limit exceeded`)
	if got := ClassifyLLMError(rlErr); got != FailureRateLimit {
		t.Fatalf("ClassifyLLMError(429 with comma) = %v, want FailureRateLimit", got)
	}
	jsonAuth := errors.New(`{"error":{"status":401,"message":"unauthorized"}}`)
	if got := ClassifyLLMError(jsonAuth); got != FailureAuth {
		t.Fatalf("ClassifyLLMError(json 401) = %v, want FailureAuth", got)
	}
	jsonRL := errors.New(`{"error":{"status":429,"message":"rate_limited"}}`)
	if got := ClassifyLLMError(jsonRL); got != FailureRateLimit {
		t.Fatalf("ClassifyLLMError(json 429) = %v, want FailureRateLimit", got)
	}

	if !containsPatternAnchored(`bad status":401`, `status":401`) {
		t.Error("anchor should match at end-of-string")
	}
	if !containsPatternAnchored(`bad status":401, more`, `status":401`) {
		t.Error("anchor should match before comma")
	}
	if containsPatternAnchored(`bad status":40139`, `status":401`) {
		t.Error("anchor must not match when followed by a digit")
	}

	if containsHTTPStatus(`status code: 42999, msg`, "429") {
		t.Error("containsHTTPStatus(42999) must not match 429")
	}
	if !containsHTTPStatus(`status code: 429, msg`, "429") {
		t.Error("containsHTTPStatus(429,) must match 429")
	}
}

func TestIssue602_R4_NonAnthropicQuotaWithResetPhraseIsQuota(t *testing.T) {
	err := errors.New(`insufficient_quota: usage limit exceeded; your limit will reset at 2026-01-01T00:00Z. upgrade your plan`)
	if got := ClassifyLLMError(err); got != FailureQuota {
		t.Fatalf("ClassifyLLMError(openai insufficient_quota + reset phrase) = %v, want FailureQuota", got)
	}
	if !IsQuotaExhaustedError(err) {
		t.Fatal("IsQuotaExhaustedError(insufficient_quota + reset phrase) = false, want true")
	}
}

func TestIssue602_R4_AnthropicWindowLimitStillRateLimit(t *testing.T) {

	err := errors.New(`rate_limit_error: your limit will reset at 5:00pm (America/Los_Angeles)`)
	if got := ClassifyLLMError(err); got != FailureRateLimit {
		t.Fatalf("ClassifyLLMError(anthropic window limit) = %v, want FailureRateLimit", got)
	}
	if IsQuotaExhaustedError(err) {
		t.Fatal("anthropic window-limit 429 must not be quota")
	}
}

func TestIssue602_R6_MixedPrefixHeadersMerge(t *testing.T) {

	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-remaining", "4")
	h.Set("anthropic-ratelimit-requests-limit", "100")
	h.Set("reratelimit-tokens-remaining", "5000")
	h.Set("reratelimit-tokens-limit", "100000")

	info := parseRateLimitHeaders(h)
	if info.RemainingTokens != 5000 {
		t.Fatalf("RemainingTokens = %d, want 5000 (mixed prefix must merge, R6)", info.RemainingTokens)
	}
	if info.LimitTokens != 100000 {
		t.Fatalf("LimitTokens = %d, want 100000", info.LimitTokens)
	}
	if info.RemainingRequests != 4 {
		t.Fatalf("RemainingRequests = %d, want 4", info.RemainingRequests)
	}
	if info.LimitRequests != 100 {
		t.Fatalf("LimitRequests = %d, want 100", info.LimitRequests)
	}

	if frac := info.TokenFractionRemaining(); frac < 0.049 || frac > 0.051 {
		t.Fatalf("TokenFractionRemaining = %v, want ~0.05", frac)
	}
}

func TestIssue602_R6_SinglePrefixStillParses(t *testing.T) {

	h := http.Header{}
	h.Set("anthropic-ratelimit-tokens-remaining", "42")
	h.Set("anthropic-ratelimit-tokens-limit", "1000")
	info := parseRateLimitHeaders(h)
	if info.RemainingTokens != 42 || info.LimitTokens != 1000 {
		t.Fatalf("single-prefix parse changed: %+v", info)
	}
	h2 := http.Header{}
	h2.Set("reratelimit-requests-remaining", "7")
	info2 := parseRateLimitHeaders(h2)
	if info2.RemainingRequests != 7 {
		t.Fatalf("new-prefix-only parse: %+v", info2)
	}
}
