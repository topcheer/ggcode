package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// #2602 defect 2: slow_down MUST increase the polling interval by 5 seconds
// (RFC 8628 section 3.5). The mock server answers slow_down twice then grants
// the token; the deltas between successive token-endpoint hits must show the
// interval growing (base 100ms-ish tick +5s each slow_down), not staying flat.
func TestIssue2602_SlowDownBacksOff(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-based poll test")
	}
	var hits []time.Time
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, time.Now())
		if atomic.AddInt32(&n, 1) <= 2 {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "refresh_token": "rt", "token_type": "bearer", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	dev := &auth.OpenCodeDeviceAuth{
		DeviceCode:              "dc",
		VerificationURIComplete: "/device",
		ExpiresIn:               300,
		Interval:                1, // 1s base interval
	}
	tok, err := pollOpenCodeToken(context.Background(), srv.URL, dev)
	if err != nil {
		t.Fatalf("pollOpenCodeToken: %v", err)
	}
	if tok.AccessToken != "at" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if len(hits) != 3 {
		t.Fatalf("want 3 endpoint hits, got %d", len(hits))
	}
	// The loop sleeps BEFORE each hit, so gaps carry the backoff applied
	// after the previous slow_down: gap1 = base(1s)+5s = ~6s,
	// gap2 = 6s+5s = ~11s. A flat ~1s gap would mean no backoff (the bug).
	gap1 := hits[1].Sub(hits[0])
	gap2 := hits[2].Sub(hits[1])
	if gap1 < 5500*time.Millisecond {
		t.Errorf("gap1 must reflect +5s backoff after first slow_down (want >=5.5s), got %v", gap1)
	}
	if gap2 < 10500*time.Millisecond {
		t.Errorf("gap2 must accumulate a second +5s backoff (want >=10.5s), got %v", gap2)
	}
	if gap1 > 8*time.Second || gap2 > 13*time.Second {
		t.Errorf("gaps unexpectedly large (backoff overshoot): gap1=%v gap2=%v", gap1, gap2)
	}
}
