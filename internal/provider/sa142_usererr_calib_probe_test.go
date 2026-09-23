package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sashabaranov/go-openai"
)

// sa142StatusErr carries an HTTP status via the duck-typed
// interface{ HTTPStatusCode() int } path exercised by hasHTTPStatus /
// extractHTTPStatus (covers providers wrapping their own status errors).
type sa142StatusErr struct {
	code int
	msg  string
}

func (e *sa142StatusErr) Error() string       { return e.msg }
func (e *sa142StatusErr) HTTPStatusCode() int { return e.code }

// TestSA142_UserFacingErrorLang: the gateway error taxonomy (auth / quota /
// rate limit / not-found / blind spot) maps to actionable bilingual copy.
func TestSA142_UserFacingErrorLang(t *testing.T) {
	cases := []struct {
		name string
		err  error
		zh   string // substring expected in the zh-CN message
		en   string // substring expected in the en message
	}{
		{
			name: "401 auth",
			err:  &openai.APIError{HTTPStatusCode: http.StatusUnauthorized, Message: "bad key"},
			zh:   "API 密钥无效",
			en:   "Invalid or missing API key",
		},
		{
			name: "403 terminated",
			err:  &openai.APIError{HTTPStatusCode: http.StatusForbidden, Message: "your account has been access_terminated"},
			zh:   "本计费周期",
			en:   "this billing cycle",
		},
		{
			name: "403 billing-cycle usage limit",
			err:  &openai.APIError{HTTPStatusCode: http.StatusForbidden, Message: "usage limit reached for this billing cycle"},
			zh:   "本计费周期",
			en:   "this billing cycle",
		},
		{
			name: "403 rate limit flavor",
			err:  &openai.APIError{HTTPStatusCode: http.StatusForbidden, Message: "rate limit hit"},
			zh:   "请求太频繁或额度不足",
			en:   "Rate limited or quota exceeded",
		},
		{
			name: "403 plain",
			err:  &openai.APIError{HTTPStatusCode: http.StatusForbidden, Message: "nope"},
			zh:   "无权访问该接口",
			en:   "Access denied",
		},
		{
			name: "404",
			err:  &openai.APIError{HTTPStatusCode: http.StatusNotFound, Message: "no route"},
			zh:   "接口不存在 (404)",
			en:   "Endpoint not found (404)",
		},
		{
			name: "429 coding-plan permanent",
			err:  &openai.APIError{HTTPStatusCode: http.StatusTooManyRequests, Message: "coding plan quota exceeded"},
			zh:   "额度已用完或套餐已过期",
			en:   "quota exhausted or plan expired",
		},
		{
			name: "429 balance permanent",
			err:  &openai.APIError{HTTPStatusCode: http.StatusTooManyRequests, Message: "余额不足，请充值"},
			zh:   "额度已用完或套餐已过期",
			en:   "quota exhausted or plan expired",
		},
		{
			name: "429 transient",
			err:  &openai.APIError{HTTPStatusCode: http.StatusTooManyRequests, Message: "slow down"},
			zh:   "请求太频繁",
			en:   "Rate limited by the server",
		},
		{
			name: "blind spot zh stem",
			err:  errors.New("totally unrecognized sa142 failure shape"),
			zh:   fallbackErrMsgZh,
			en:   fallbackErrMsgEn,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserFacingErrorLang(tc.err, "zh-CN"); !strings.Contains(got, tc.zh) {
				t.Fatalf("zh message %q missing %q", got, tc.zh)
			}
			if got := UserFacingErrorLang(tc.err, "en"); !strings.Contains(got, tc.en) {
				t.Fatalf("en message %q missing %q", got, tc.en)
			}
		})
	}
	if got := UserFacingErrorLang(nil, "zh-CN"); got != "" {
		t.Fatalf("nil error must map to empty string, got %q", got)
	}
	// Wrapped errors unwrap once and still classify.
	wrapped := fmt.Errorf("outer: %w", &openai.APIError{HTTPStatusCode: http.StatusUnauthorized, Message: "k"})
	if got := UserFacingErrorLang(wrapped, "en"); !strings.Contains(got, "Invalid or missing API key") {
		t.Fatalf("wrapped 401 misclassified: %q", got)
	}
	// Duck-typed HTTPStatusCode carriers classify too.
	duck := &sa142StatusErr{code: http.StatusUnauthorized, msg: "custom wrapper"}
	if got := UserFacingErrorLang(duck, "en"); !strings.Contains(got, "Invalid or missing API key") {
		t.Fatalf("duck-typed status error misclassified: %q", got)
	}
}

// TestSA142_TruncateRawError: truncation respects the byte budget, marks the
// cut, and never splits a multi-byte rune (#1720).
func TestSA142_TruncateRawError(t *testing.T) {
	short := truncateRawError("tiny")
	if short != "tiny" {
		t.Fatalf("short raw = %q", short)
	}
	long := strings.Repeat("a", blindSpotRawLimit*2)
	got := truncateRawError(long)
	if !strings.HasSuffix(got, "…(truncated)") || len(got) > blindSpotRawLimit+len("…(truncated)")+10 {
		t.Fatalf("long raw truncation wrong: len=%d suffix=%q", len(got), got[len(got)-20:])
	}
	cjk := strings.Repeat("汉", blindSpotRawLimit)
	gotCJK := truncateRawError(cjk)
	if !utf8.ValidString(gotCJK) {
		t.Fatal("CJK truncation produced invalid UTF-8 (mid-rune cut)")
	}
	if !strings.HasSuffix(gotCJK, "…(truncated)") {
		t.Fatalf("CJK truncation missing marker: %q", gotCJK[len(gotCJK)-30:])
	}
}

// TestSA142_BlindSpotAndStatus: IsBlindSpotError detects only the fallback
// branch; extractHTTPStatus reads every known carrier.
func TestSA142_BlindSpotAndStatus(t *testing.T) {
	if IsBlindSpotError(nil) {
		t.Fatal("nil error is not a blind spot")
	}
	if IsBlindSpotError(&openai.APIError{HTTPStatusCode: 401, Message: "x"}) {
		t.Fatal("classified 401 is not a blind spot")
	}
	if !IsBlindSpotError(errors.New("xyzzy unclassifiable")) {
		t.Fatal("unclassifiable error must be a blind spot")
	}
	if got := extractHTTPStatus(nil); got != 0 {
		t.Fatalf("extractHTTPStatus(nil) = %d", got)
	}
	if got := extractHTTPStatus(errors.New("plain")); got != 0 {
		t.Fatalf("extractHTTPStatus(plain) = %d", got)
	}
	if got := extractHTTPStatus(&openai.APIError{HTTPStatusCode: 503}); got != 503 {
		t.Fatalf("extractHTTPStatus(openai) = %d", got)
	}
	if got := extractHTTPStatus(&sa142StatusErr{code: 403}); got != 403 {
		t.Fatalf("extractHTTPStatus(duck) = %d", got)
	}
	if !hasHTTPStatus(&sa142StatusErr{code: 403}, 403) {
		t.Fatal("hasHTTPStatus missed the duck-typed carrier")
	}
	if hasHTTPStatus(&sa142StatusErr{code: 403}, 401) {
		t.Fatal("hasHTTPStatus matched the wrong status")
	}
}

// TestSA142_TokenCalibrator: ratio learning, skip/failure bookkeeping, and
// permanent disable.
func TestSA142_TokenCalibrator(t *testing.T) {
	c := newTokenCountCalibrator()
	if got := c.currentRatio(); got != 1.0 {
		t.Fatalf("initial ratio = %v, want 1.0", got)
	}
	// Invalid samples are ignored entirely.
	c.applyResult(0, 100)
	c.applyResult(100, 0)
	c.applyResult(-5, -5)
	if got := c.currentRatio(); got != 1.0 {
		t.Fatalf("invalid samples moved the ratio to %v", got)
	}
	// First valid sample is accepted directly (clamped).
	c.applyResult(100, 130)
	first := c.currentRatio()
	if first != clampRatio(1.3) {
		t.Fatalf("first calibration ratio = %v, want clamp(1.3)", first)
	}
	// Incremental average: 70% old + 30% new.
	c.applyResult(100, 100)
	want := clampRatio(first*0.7 + 1.0*0.3)
	if got := c.currentRatio(); got != want {
		t.Fatalf("incremental ratio = %v, want %v", got, want)
	}

	// recordSkip advances the cadence clock without counting a failure.
	c2 := newTokenCountCalibrator()
	c2.recordSkip()
	c2.mu.Lock()
	skipped := !c2.lastCalibrate.IsZero()
	c2.mu.Unlock()
	if !skipped {
		t.Fatal("recordSkip did not advance lastCalibrate")
	}

	// recordFailure backs off, then disables after the threshold.
	c3 := newTokenCountCalibrator()
	c3.recordFailure()
	c3.mu.Lock()
	backingOff := !c3.lastFailure.IsZero() && c3.enabled
	c3.mu.Unlock()
	if !backingOff {
		t.Fatal("first failure must start the backoff window while enabled")
	}
	for i := 0; i < calibrateMaxConsecutiveFailures; i++ {
		c3.recordFailure()
	}
	if c3.enabled || c3.currentRatio() != 1.0 {
		t.Fatal("calibrator must disable and reset ratio after consecutive failures")
	}
	c3.recordSkip() // no-op on a disabled calibrator
	c3.recordFailure()

	// disable() is permanent and resets to the raw local ratio.
	c4 := newTokenCountCalibrator()
	c4.disable()
	if c4.enabled || c4.currentRatio() != 1.0 {
		t.Fatal("disable() must turn calibration off")
	}
	c4.disable() // second call is a no-op

	// clampRatio bounds.
	if clampRatio(-1) != ratioClampMin || clampRatio(1e9) != ratioClampMax || clampRatio(0.5) != 0.5 {
		t.Fatalf("clampRatio bounds wrong: %v %v %v", clampRatio(-1), clampRatio(1e9), clampRatio(0.5))
	}
}

// TestSA142_CopilotHelpers: request inspection and resolved-endpoint checks.
func TestSA142_CopilotHelpers(t *testing.T) {
	if err := validateCopilotResolved("", "key"); err == nil {
		t.Fatal("empty baseURL must be rejected")
	}
	if err := validateCopilotResolved("  ", "key"); err == nil {
		t.Fatal("blank baseURL must be rejected")
	}
	if err := validateCopilotResolved("https://x", ""); err == nil {
		t.Fatal("empty apiKey must be rejected")
	}
	if err := validateCopilotResolved("https://x", "key"); err != nil {
		t.Fatalf("valid resolved rejected: %v", err)
	}

	isAgent, isVision := inspectCopilotRequest([]byte(`{"messages":[{"role":"assistant","content":"hi"}]}`))
	if !isAgent || isVision {
		t.Fatalf("assistant-last string content: agent=%v vision=%v, want true/false", isAgent, isVision)
	}
	isAgent, isVision = inspectCopilotRequest([]byte(`{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`))
	if isAgent || !isVision {
		t.Fatalf("user image content: agent=%v vision=%v, want false/true", isAgent, isVision)
	}
	isAgent, isVision = inspectCopilotRequest([]byte(`{"messages":[{"role":"user","content":[{"type":"text"}]}]}`))
	if isAgent || isVision {
		t.Fatalf("plain user content: agent=%v vision=%v, want false/false", isAgent, isVision)
	}
	if isAgent, isVision = inspectCopilotRequest([]byte(`not-json`)); isAgent || isVision {
		t.Fatalf("malformed body: agent=%v vision=%v, want false/false", isAgent, isVision)
	}

	// SetImpersonatedUA reaches the copilot round-tripper; a bare provider
	// must not panic.
	p := NewCopilotProvider("k", "m", 10, "http://127.0.0.1:9")
	p.SetImpersonatedUA("sa142-ua")
	if rt, ok := p.OpenAIProvider.transport.base.(*copilotHeaderRoundTripper); !ok || rt.impersonatedUA != "sa142-ua" {
		t.Fatalf("impersonated UA not installed: ok=%v ua=%q", ok, rt.impersonatedUA)
	}
	bare := &CopilotProvider{}
	bare.SetImpersonatedUA("x") // nil inner provider/transport: no panic
}

// TestSA142_MakeProbeKey: components are trimmed and pipe-joined.
func TestSA142_MakeProbeKey(t *testing.T) {
	if got := MakeProbeKey(" v ", " u ", " m "); got != "v|u|m" {
		t.Fatalf("MakeProbeKey = %q", got)
	}
}

// TestSA142_ProbeContextWindow: guard rails, cache hit, known-model hit, and
// the background probe failing closed on a dead provider.
func TestSA142_ProbeContextWindow(t *testing.T) {
	// nil provider: no panic, no callback.
	called := false
	ProbeContextWindow(context.Background(), nil, "vendor", "url", "model", func(ProbeResult) { called = true })
	if called {
		t.Fatal("nil provider must not invoke onResult")
	}
	// Empty vendor/model: rejected before any lookup.
	p := &mockProvider{name: "mock", chatErr: errors.New("down")}
	ProbeContextWindow(context.Background(), p, "  ", "url", "model", func(ProbeResult) { called = true })
	if called {
		t.Fatal("empty vendor must not invoke onResult")
	}
	ProbeContextWindow(context.Background(), p, "vendor", "url", " ", func(ProbeResult) { called = true })
	if called {
		t.Fatal("empty model must not invoke onResult")
	}

	// Cache hit: synchronous, FromCache=true.
	cacheKey := MakeProbeKey("sa142-cache-vendor", "url", "model")
	SetProbeCache(cacheKey, 555000)
	resCh := make(chan ProbeResult, 1)
	ProbeContextWindow(context.Background(), p, "sa142-cache-vendor", "url", "model", func(r ProbeResult) { resCh <- r })
	select {
	case r := <-resCh:
		if !r.FromCache || r.ContextWindow != 555000 {
			t.Fatalf("cache hit = %+v, want cached 555000", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cache-hit probe did not call back synchronously")
	}

	// Known model table: instant, no API call, cached for next time.
	resCh = make(chan ProbeResult, 1)
	ProbeContextWindow(context.Background(), p, "sa142-known-vendor", "url", "claude-sonnet-4", func(r ProbeResult) { resCh <- r })
	select {
	case r := <-resCh:
		if r.FromCache || r.ContextWindow != 200_000 {
			t.Fatalf("known model = %+v, want 200000 not-from-cache", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("known-model probe did not call back")
	}

	// Unknown model + failing provider: the background probe completes with
	// zero (failure closed) instead of hanging.
	resCh = make(chan ProbeResult, 1)
	ProbeContextWindow(context.Background(), p, "sa142-bg-vendor", "url", "unknown-sa142-model", func(r ProbeResult) { resCh <- r })
	select {
	case r := <-resCh:
		if r.FromCache || r.ContextWindow != 0 {
			t.Fatalf("background failure = %+v, want 0 not-from-cache", r)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("background probe did not finish")
	}
}
