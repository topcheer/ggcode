package acp

// #672 (#668 regression): permanent errors (DNS resolution failure, TLS,
// 4xx-non-429, non-JSON body) must abort the device-flow token poll
// immediately instead of retrying for the whole device-code lifetime, and a
// 200 with an empty token must fail instead of being persisted as success.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Permanent HTTP status: a 404 (wrong endpoint) must abort the flow on the
// first poll instead of retrying ~180 times across the 15-minute lifetime.
func TestIssue672PermanentHTTP404AbortsImmediately(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "no such endpoint", http.StatusNotFound)
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	start := time.Now()
	_, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 900,
	})
	if err == nil {
		t.Fatalf("404 must abort the flow")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("permanent 404 must not be retried, got %d calls", calls)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("permanent error burned %v polling (regression of #672 fix)", elapsed)
	}
}

// Permanent transport: DNS resolution failure (NXDOMAIN) must classify as
// permanent and abort — exercised directly with the error shape
// http.Client.Do produces, no live network needed.
func TestIssue672ClassifyTransportErrorPermanentDNS(t *testing.T) {
	raw := &url.Error{Op: "Post", URL: "https://ghost.invalid/token", Err: &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{Err: "no such host", Name: "ghost.invalid", IsNotFound: true},
	}}
	wrapped := classifyTransportError(fmt.Errorf("POST access_token: %w", raw))
	if _, ok := wrapped.(*permanentDeviceFlowError); !ok {
		t.Fatalf("NXDOMAIN must classify permanent, got %T: %v", wrapped, wrapped)
	}
	if classifyDevicePollError(wrapped) != devicePollAbort {
		t.Fatalf("permanent transport error must abort the poll loop")
	}
}

// Permanent transport: TLS certificate verification failure.
func TestIssue672ClassifyTransportErrorPermanentTLS(t *testing.T) {
	raw := &url.Error{Op: "Post", URL: "https://self-signed.invalid/token", Err: fmt.Errorf(
		"tls: failed to verify certificate: %w", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}})}
	wrapped := classifyTransportError(fmt.Errorf("POST access_token: %w", raw))
	if _, ok := wrapped.(*permanentDeviceFlowError); !ok {
		t.Fatalf("TLS verification failure must classify permanent, got %T: %v", wrapped, wrapped)
	}
	if classifyDevicePollError(wrapped) != devicePollAbort {
		t.Fatalf("TLS failure must abort the poll loop")
	}
}

// Transient transport: a timeout stays retryable so a proxy blip cannot
// waste an already-entered user_code (#668 behavior preserved).
func TestIssue672ClassifyTransportErrorTimeoutTransient(t *testing.T) {
	raw := &url.Error{Op: "Post", URL: "https://github.com/token", Err: context.DeadlineExceeded}
	wrapped := classifyTransportError(fmt.Errorf("POST access_token: %w", raw))
	if _, ok := wrapped.(*transientDeviceFlowError); !ok {
		t.Fatalf("timeout must classify transient, got %T: %v", wrapped, wrapped)
	}
	if classifyDevicePollError(wrapped) != devicePollContinue {
		t.Fatalf("transient transport error must keep polling")
	}
}

// HTTP 429 and 5xx are transient: one failure then success must still yield
// the token.
func TestIssue672Transient429And500Retried(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		code := code
		t.Run(fmt.Sprintf("code=%d", code), func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(code)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"access_token":"tok-672","token_type":"bearer"}`)
			}))
			defer srv.Close()

			ah := &AuthHandler{accessTokenURL: srv.URL}
			token, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
				DeviceCode: "dc", Interval: 1, ExpiresIn: 30,
			})
			if err != nil || token != "tok-672" {
				t.Fatalf("HTTP %d must be retried, got token=%q err=%v", code, token, err)
			}
			if calls < 2 {
				t.Fatalf("expected retry after HTTP %d", code)
			}
		})
	}
}

// 200 with an empty token (e.g. `{}`) must be a terminal failure, never a
// success whose empty token gets persisted.
func TestIssue672EmptyTokenOn200IsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{}`) // 200, no error field, no access_token
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	// Direct checkToken: must NOT return ("", nil) — that path persisted
	// the empty token and reported the flow successful (#672 defect 2).
	_, err := ah.checkToken(context.Background(), "dc")
	if err == nil {
		t.Fatalf("200 with empty token must be an error, not success")
	}
	if classifyDevicePollError(err) != devicePollAbort {
		t.Fatalf("empty-token 200 must be terminal, classified %v", classifyDevicePollError(err))
	}
	// Loop level: pollForToken must abort rather than return an empty token.
	_, err = ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 30,
	})
	if err == nil {
		t.Fatalf("pollForToken must abort on empty-token 200 (empty token would be persisted)")
	}
	if !strings.Contains(err.Error(), "access_token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A 200 whose body is not JSON (HTML error page) is a permanent
// misconfiguration and must abort on the first poll.
func TestIssue672NonJSON200AbortsImmediately(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, "<html><body>gateway login page</body></html>")
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	_, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 900,
	})
	if err == nil {
		t.Fatalf("non-JSON 200 must abort the flow")
	}
	if calls != 1 {
		t.Fatalf("non-JSON 200 must not be retried, got %d calls", calls)
	}
}

// The consecutive-transient streak cap bounds the retry loop (#672).
func TestIssue672TransientStreakCap(t *testing.T) {
	var st transientStreakTracker
	// authorization_pending does not count toward the streak.
	for i := 0; i < maxConsecutiveTransientPollErrors*2; i++ {
		if st.record(errDeviceAuthPending) {
			t.Fatalf("authorization_pending must never exhaust the budget")
		}
	}
	for i := 1; i < maxConsecutiveTransientPollErrors; i++ {
		if st.record(&transientDeviceFlowError{err: errors.New("blip")}) {
			t.Fatalf("streak exhausted early at %d", i)
		}
	}
	if !st.record(&transientDeviceFlowError{err: errors.New("blip")}) {
		t.Fatalf("streak must be exhausted after %d consecutive transient failures", maxConsecutiveTransientPollErrors)
	}
}

// slow_down interval growth stays cumulative (RFC 8628 §3.5, #668) but is
// capped (#672 secondary).
func TestIssue672SlowDownIntervalCapped(t *testing.T) {
	if got := growDevicePollInterval(5); got != 10 {
		t.Fatalf("RFC 8628 cumulative growth broken: 5→%d (want 10)", got)
	}
	cur := 5
	for i := 0; i < 30; i++ {
		cur = growDevicePollInterval(cur)
	}
	if cur != maxDevicePollIntervalSeconds {
		t.Fatalf("interval must clamp to %d, got %d", maxDevicePollIntervalSeconds, cur)
	}
}
