package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// ============================================================================
// #602 R1 — string-fallback retryability must match the typed HTTP classifier
// for 402 (payment required) and 413 (payload too large).
// ============================================================================

func TestIssue602_R1_StringFallback402NotRetryable(t *testing.T) {
	// Probe from the issue: an OpenAI-compatible relay error that carries
	// "status code: 402" in its message but no typed status code. The typed
	// path (isRetryableHTTPStatus) already says permanent; the string path
	// must agree instead of burning 20 retries × backoff on a billing error.
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
	// The divergence class (#306/#518/#267/#602): for every excluded status,
	// both paths must agree the error is non-retryable.
	for _, code := range []int{400, 401, 402, 403, 404, 413, 422} {
		if isRetryableHTTPStatus(code) {
			t.Errorf("typed %d: isRetryableHTTPStatus = true, want false", code)
		}
		strErr := fmt.Errorf("error, status code: %d, message: boom", code)
		if isRetryable(strErr) {
			t.Errorf("string %d: isRetryable = true, want false", code)
		}
	}
	// Genuinely transient statuses remain retryable on both paths.
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

// ============================================================================
// #602 R2 — the failover stream wrapper must observe consumer cancellation.
// Before the fix every channel send was unguarded; a cancelled consumer
// parked the wrapper goroutine on `out <-`, parked the drain goroutine, and
// filled the provider's buffered channel — three stuck layers per turn.
// ============================================================================

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
	// Deliberately never close(ch): stream hangs after the buffered events.
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

	// Read one event, then cancel like an agent abandoning the turn.
	<-stream
	cancel()

	// The wrapper must exit its send/select loop and close `out` shortly
	// after cancellation. Before #602(R2) it parked forever on `out <-`.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return // wrapper exited cleanly — PASS
			}
		case <-deadline:
			t.Fatal("stream channel not closed within 3s of ctx cancellation — wrapper goroutine leaked (R2)")
		}
	}
}

func TestIssue602_R2_FailoverDrainSurvivesCancellation(t *testing.T) {
	// Primary emits an error event then hangs (never closes); the fallback
	// kicks in and also hangs. Cancelling mid-fallback must release both the
	// wrapper and the drain goroutine.
	primary := &errThenHangProvider{name: "primary"}
	fallback := &mockStreamProvider{name: "fallback", events: 1}
	fp := NewFallbackProvider(primary, fallback, "primary -> fallback")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := fp.ChatStream(ctx, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// Drain the failover system notice + first fallback event.
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
				return // PASS
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
	// Quota class triggers immediate failover (shouldFailover), unlike a
	// transient rate-limit error which needs failoverThreshold consecutive
	// hits — one error event would never arm the fallback under test.
	ch <- StreamEvent{Type: StreamEventError, Error: errors.New("insufficient_quota: usage limit exceeded, upgrade your plan")}
	return ch, nil // never closed
}
func (m *errThenHangProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return 10, nil
}

// ============================================================================
// #602 R3 — digit-piercing status anchors. 5-digit coincidences like
// `"status":40139` must NOT classify as 401 auth (sticky failover) or
// `"status":42999` as 429 rate limit.
// ============================================================================

func TestIssue602_R3_FiveDigitStatusDoesNotPierceAuthAnchor(t *testing.T) {
	err := errors.New(`request failed: {"error":{"message":"invalid model id","type":"invalid_request_error","param":null,"code":"model_not_found","status":40139}}`)
	if got := ClassifyLLMError(err); got != FailureTransient {
		t.Fatalf("ClassifyLLMError(status=40139) = %v, want FailureTransient (not auth)", got)
	}
}

func TestIssue602_R3_FiveDigitStatusDoesNotPierceRateLimitAnchor(t *testing.T) {
	// "unavailable" deliberately avoids every rateLimitKeywords entry so
	// ONLY the digit-pierced `status":429` anchor can explain a rate-limit
	// classification.
	err := errors.New(`upstream responded {"error":{"message":"model unavailable for account tier","status":42999}}`)
	if got := ClassifyLLMError(err); got != FailureTransient {
		t.Fatalf("ClassifyLLMError(status=42999) = %v, want FailureTransient (not rate limit)", got)
	}
}

func TestIssue602_R3_RealFourDigitStatusStillMatches(t *testing.T) {
	// Positive controls: the exact anchor forms the SDKs emit must still hit.
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
	// End-of-string and non-digit boundary cases.
	if !containsPatternAnchored(`bad status":401`, `status":401`) {
		t.Error("anchor should match at end-of-string")
	}
	if !containsPatternAnchored(`bad status":401, more`, `status":401`) {
		t.Error("anchor should match before comma")
	}
	if containsPatternAnchored(`bad status":40139`, `status":401`) {
		t.Error("anchor must not match when followed by a digit")
	}
	// containsHTTPStatus shares the anchoring (retry path).
	if containsHTTPStatus(`status code: 42999, msg`, "429") {
		t.Error("containsHTTPStatus(42999) must not match 429")
	}
	if !containsHTTPStatus(`status code: 429, msg`, "429") {
		t.Error("containsHTTPStatus(429,) must match 429")
	}
}

// ============================================================================
// #602 R4 — window-limit exclusion is Anthropic-only. A non-Anthropic
// billing-cycle quota error that mentions "your limit will reset at" is
// permanent quota, not a recoverable rate limit.
// ============================================================================

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
	// Positive control: genuine Anthropic window-limit 429s stay transient.
	err := errors.New(`rate_limit_error: your limit will reset at 5:00pm (America/Los_Angeles)`)
	if got := ClassifyLLMError(err); got != FailureRateLimit {
		t.Fatalf("ClassifyLLMError(anthropic window limit) = %v, want FailureRateLimit", got)
	}
	if IsQuotaExhaustedError(err) {
		t.Fatal("anthropic window-limit 429 must not be quota")
	}
}

// ============================================================================
// #602 R5 — static: unreachable guard removed from anthropic.go (no probe;
// verified by inspection: every loop iteration ends in continue/return/break
// after an unconditional usage assignment).
// ============================================================================

// ============================================================================
// #602 R6 — mixed-prefix Anthropic rate-limit headers must merge instead of
// truncating after the first non-empty prefix group.
// ============================================================================

func TestIssue602_R6_MixedPrefixHeadersMerge(t *testing.T) {
	// Old prefix carries requests info; new prefix carries tokens info.
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
	// Token fraction must reflect reality, not the pre-fix -1 → 100% illusion.
	if frac := info.TokenFractionRemaining(); frac < 0.049 || frac > 0.051 {
		t.Fatalf("TokenFractionRemaining = %v, want ~0.05", frac)
	}
}

func TestIssue602_R6_SinglePrefixStillParses(t *testing.T) {
	// Regression guard: single-prefix responses parse exactly as before.
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
