package agent

import (
	"testing"
	"time"
)

// The sawtooth sequence doubles from 1s while below the cap, emits the cap
// once, then restarts at 1s (#rate-limit-sawtooth, user directive).
func TestRateLimitBackoffDelayDefaultCap(t *testing.T) {
	cap5m := 5 * time.Minute
	// One full cycle: 1,2,4,8,16,32,64,128,256,300
	want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300}
	for i, w := range want {
		if got := rateLimitBackoffDelay(i+1, cap5m); got != w*time.Second {
			t.Fatalf("attempt %d: got %v, want %v", i+1, got, w*time.Second)
		}
	}
	// Cycle restarts: attempt 11 -> 1s again, attempt 20 -> cap again.
	if got := rateLimitBackoffDelay(11, cap5m); got != 1*time.Second {
		t.Fatalf("attempt 11 (cycle restart): got %v, want 1s", got)
	}
	if got := rateLimitBackoffDelay(20, cap5m); got != cap5m {
		t.Fatalf("attempt 20 (cap step): got %v, want %v", got, cap5m)
	}
	// Sustained outage keeps cycling rather than pinning at the cap forever.
	if got := rateLimitBackoffDelay(21, cap5m); got != 1*time.Second {
		t.Fatalf("attempt 21 (second cycle restart): got %v, want 1s", got)
	}
}

// Small caps produce proportionally short cycles (env override path).
func TestRateLimitBackoffDelaySmallCap(t *testing.T) {
	// cap 3s: 1,2,3 then restart.
	for n, w := range map[int]time.Duration{1: 1, 2: 2, 3: 3, 4: 1, 5: 2} {
		if got := rateLimitBackoffDelay(n, 3*time.Second); got != w*time.Second {
			t.Fatalf("cap 3s attempt %d: got %v, want %v", n, got, w*time.Second)
		}
	}
	// cap 1s or below degenerates to the constant cap.
	if got := rateLimitBackoffDelay(7, 1*time.Second); got != 1*time.Second {
		t.Fatalf("cap 1s attempt 7: got %v, want 1s", got)
	}
}
