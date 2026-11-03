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
		Interval:                1,
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
