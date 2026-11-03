package acp

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

func TestIssue672EmptyTokenOn200IsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{}`)
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	_, err := ah.checkToken(context.Background(), "dc")
	if err == nil {
		t.Fatalf("200 with empty token must be an error, not success")
	}
	if classifyDevicePollError(err) != devicePollAbort {
		t.Fatalf("empty-token 200 must be terminal, classified %v", classifyDevicePollError(err))
	}
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

func TestIssue672TransientStreakCap(t *testing.T) {
	var st transientStreakTracker
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
