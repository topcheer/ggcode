package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Feature tests for issue #560 A-2: the OAuth callback authorization code
// must be single-consumption. Previously the success branch of the callback
// handler neither closed the listener nor set a completion flag, and
// CompleteAnthropicOAuth's deferred flow.Close() only ran after the token
// exchange (up to 30s) — within that window a second callback carrying the
// same valid state could deliver another code to WaitForClaudeAuthCode
// (probe-verified "REPLAY LEAK", not remotely exploitable since state is
// 32 random bytes, but a defensive state-machine defect).

// noRedirectClient returns an HTTP client that does not follow redirects,
// so the callback's 302 to the success page is observed directly.
func noRedirectClient560() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}
}

// TestIssue560OAuthCodeSingleConsumption verifies that after the first
// valid callback delivers the authorization code:
//  1. the listener is shut down promptly (no waiting for the token
//     exchange window), and
//  2. a replayed callback with the same valid state cannot deliver a
//     second code to another WaitForClaudeAuthCode waiter on the same flow.
func TestIssue560OAuthCodeSingleConsumption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flow, err := StartClaudeOAuthFlow(ctx)
	if err != nil {
		t.Skipf("cannot bind local OAuth listener in this environment: %v", err)
	}
	defer flow.Close()

	client := noRedirectClient560()
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d%s?%s", flow.Port, claudeOAuthCallbackPath,
		url.Values{"code": {"first-code"}, "state": {flow.State}}.Encode())

	type waitResult struct {
		code string
		err  error
	}

	// Waiter A consumes the first delivery.
	doneA := make(chan waitResult, 1)
	go func() {
		code, _, werr := WaitForClaudeAuthCode(ctx, flow)
		doneA <- waitResult{code, werr}
	}()

	resp, err := client.Get(callbackURL)
	if err != nil {
		t.Fatalf("first callback request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("first callback: expected 302, got %d", resp.StatusCode)
	}

	select {
	case r := <-doneA:
		if r.err != nil {
			t.Fatalf("first delivery returned error: %v", r.err)
		}
		if r.code != "first-code" {
			t.Fatalf("first delivery: expected "+"first-code, got %q", r.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first callback was not delivered to waiter within 5s")
	}

	// Give the async listener shutdown a moment to take effect, then
	// replay a callback with the same valid state.
	time.Sleep(200 * time.Millisecond)

	// Waiter B on the same flow must NOT receive a second code.
	ctxB, cancelB := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancelB()
	doneB := make(chan waitResult, 1)
	go func() {
		code, _, werr := WaitForClaudeAuthCode(ctxB, flow)
		doneB <- waitResult{code, werr}
	}()

	// The replayed request may be refused (listener down) or still served
	// (race with shutdown) — either way it must not deliver a code.
	if resp2, err2 := client.Get(callbackURL); err2 == nil {
		resp2.Body.Close()
	}

	select {
	case r := <-doneB:
		if r.err == nil {
			t.Fatalf("second code delivered to waiter B — replay leak still present (code=%q)", r.code)
		}
		// Expected: waiter B timed out (ctx error), no second delivery.
	case <-time.After(3 * time.Second):
		t.Fatal("waiter B neither received a code nor timed out — unexpected hang")
	}
}

// TestIssue560ReplayedCallbackGetsGone verifies that a replayed callback
// that is still served (before the listener shutdown completes) is rejected
// with 410 Gone rather than re-writing the success redirect.
func TestIssue560ReplayedCallbackGetsGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flow, err := StartClaudeOAuthFlow(ctx)
	if err != nil {
		t.Skipf("cannot bind local OAuth listener in this environment: %v", err)
	}
	defer flow.Close()

	client := noRedirectClient560()
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d%s?%s", flow.Port, claudeOAuthCallbackPath,
		url.Values{"code": {"code-x"}, "state": {flow.State}}.Encode())

	resp, err := client.Get(callbackURL)
	if err != nil {
		t.Fatalf("first callback request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("first callback: expected 302, got %d", resp.StatusCode)
	}

	// Drain the delivered result so the buffer is empty — the replay could
	// deliver if the guard were missing.
	select {
	case <-flow.callbackCh:
	case <-time.After(2 * time.Second):
		t.Fatal("first result not delivered")
	}

	// Replay immediately: even if the listener is still accepting during
	// the shutdown race, the handler must reject with 410 (or the connection
	// is refused, which also passes).
	resp2, err2 := client.Get(callbackURL)
	if err2 != nil {
		return // listener already shut down — replay blocked at transport level
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusGone {
		t.Fatalf("replayed callback: expected 410 Gone or connection refused, got %d", resp2.StatusCode)
	}
}
