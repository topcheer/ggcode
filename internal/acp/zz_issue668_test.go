package acp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #668 defect 1: a transient network error during token polling must NOT
// abort the whole device flow — the user may already have entered the
// user_code. The loop keeps polling until the token arrives or the device
// code expires; only terminal OAuth errors abort.
func TestIssue668TransientNetworkErrorRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			// Simulate a proxy blip: close the connection mid-request.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("server not hijackable")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close()
			return
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"tok-668","token_type":"bearer","scope":"read:user"}`)
		}
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	// Short interval so the test is fast; small expiry for the safety net.
	token, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 30,
	})
	if err != nil {
		t.Fatalf("pollForToken should survive a transient network error, got: %v", err)
	}
	if token != "tok-668" {
		t.Fatalf("token = %q", token)
	}
	if calls < 2 {
		t.Fatalf("expected retry after network error, got %d calls", calls)
	}
}

// #668 defect 1: terminal OAuth errors (access_denied / expired_token) still
// abort the flow immediately.
func TestIssue668TerminalOAuthErrorAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"error":"access_denied"}`)
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	_, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 30,
	})
	if err == nil {
		t.Fatalf("access_denied must abort the flow")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// #668 defect 2: slow_down must grow the polling interval cumulatively per
// RFC 8628 §3.5 (base 1s → 6s → 11s ...), not reset to a constant base+5.
func TestIssue668SlowDownBackoffAccumulates(t *testing.T) {
	var calls int
	var gaps []time.Duration
	var last time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		if calls > 0 {
			gaps = append(gaps, now.Sub(last))
		}
		last = now
		calls++
		switch calls {
		case 1:
			fmt.Fprintf(w, `{"error":"slow_down"}`)
		case 2:
			fmt.Fprintf(w, `{"error":"slow_down"}`)
		default:
			fmt.Fprintf(w, `{"access_token":"tok","token_type":"bearer"}`)
		}
		w.Header().Set("Content-Type", "application/json")
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	token, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc", Interval: 1, ExpiresIn: 60,
	})
	if err != nil || token != "tok" {
		t.Fatalf("pollForToken = %q, %v", token, err)
	}
	if len(gaps) != 2 {
		t.Fatalf("expected 2 inter-call gaps, got %v", gaps)
	}
	// gap[0] = poll1 → poll2: after slow_down, interval 1+5=6s
	// gap[1] = poll2 → poll3: after a second slow_down, interval 6+5=11s
	// Tolerate clock slack; require the second gap to be clearly longer than
	// a constant base+5 reset (which would also be ~6s).
	if gaps[1] <= gaps[0]+2*time.Second {
		t.Fatalf("slow_down backoff did not accumulate: gaps = %v", gaps)
	}
	if gaps[1] < 9*time.Second {
		t.Fatalf("second interval should be ~11s, gaps = %v", gaps)
	}
}

// classifyDevicePollError routing table.
func TestIssue668ClassifyDevicePollError(t *testing.T) {
	if got := classifyDevicePollError(errDeviceAuthPending); got != devicePollContinue {
		t.Fatalf("pending → continue, got %v", got)
	}
	if got := classifyDevicePollError(errDeviceSlowDown); got != devicePollSlowDown {
		t.Fatalf("slow_down → grow interval, got %v", got)
	}
	if got := classifyDevicePollError(&terminalDeviceFlowError{err: errors.New("access_denied")}); got != devicePollAbort {
		t.Fatalf("terminal OAuth error → abort, got %v", got)
	}
	// Wrapped network errors are non-terminal.
	netErr := fmt.Errorf("POST access_token: %w", errors.New("connection reset by peer"))
	if got := classifyDevicePollError(netErr); got != devicePollContinue {
		t.Fatalf("transient network error → continue, got %v", got)
	}
	// Wrapped sentinels still route by errors.Is.
	if got := classifyDevicePollError(fmt.Errorf("wrap: %w", errDeviceSlowDown)); got != devicePollSlowDown {
		t.Fatalf("wrapped slow_down → grow interval, got %v", got)
	}
}

// Context cancellation still terminates the poll loop promptly.
func TestIssue668PollCancelContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"error":"authorization_pending"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	ah := &AuthHandler{accessTokenURL: srv.URL}
	start := time.Now()
	_, err := ah.pollForToken(ctx, &DeviceCodeResponse{DeviceCode: "dc", Interval: 1, ExpiresIn: 30})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cancel took too long: %v", time.Since(start))
	}
}
