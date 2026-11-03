package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func noRedirectClient560() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}
}

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

	time.Sleep(200 * time.Millisecond)

	ctxB, cancelB := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancelB()
	doneB := make(chan waitResult, 1)
	go func() {
		code, _, werr := WaitForClaudeAuthCode(ctxB, flow)
		doneB <- waitResult{code, werr}
	}()

	if resp2, err2 := client.Get(callbackURL); err2 == nil {
		resp2.Body.Close()
	}

	select {
	case r := <-doneB:
		if r.err == nil {
			t.Fatalf("second code delivered to waiter B — replay leak still present (code=%q)", r.code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiter B neither received a code nor timed out — unexpected hang")
	}
}

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

	select {
	case <-flow.callbackCh:
	case <-time.After(2 * time.Second):
		t.Fatal("first result not delivered")
	}

	resp2, err2 := client.Get(callbackURL)
	if err2 != nil {
		return
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusGone {
		t.Fatalf("replayed callback: expected 410 Gone or connection refused, got %d", resp2.StatusCode)
	}
}
