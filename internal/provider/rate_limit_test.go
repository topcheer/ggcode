package provider

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRateLimitHeaders_OpenAI(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "5000")
	h.Set("x-ratelimit-limit-tokens", "160000")
	h.Set("x-ratelimit-remaining-requests", "4999")
	h.Set("x-ratelimit-remaining-tokens", "159832")
	h.Set("x-ratelimit-reset-requests", "120ms")
	h.Set("x-ratelimit-reset-tokens", "6h0m0s")

	info := parseRateLimitHeaders(h)

	if info.LimitRequests != 5000 {
		t.Errorf("LimitRequests: got %d, want 5000", info.LimitRequests)
	}
	if info.LimitTokens != 160000 {
		t.Errorf("LimitTokens: got %d, want 160000", info.LimitTokens)
	}
	if info.RemainingRequests != 4999 {
		t.Errorf("RemainingRequests: got %d, want 4999", info.RemainingRequests)
	}
	if info.RemainingTokens != 159832 {
		t.Errorf("RemainingTokens: got %d, want 159832", info.RemainingTokens)
	}
	if info.ResetRequests != 120*time.Millisecond {
		t.Errorf("ResetRequests: got %v, want 120ms", info.ResetRequests)
	}
	if info.ResetTokens != 6*time.Hour {
		t.Errorf("ResetTokens: got %v, want 6h", info.ResetTokens)
	}
}

func TestParseRateLimitHeaders_Anthropic(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-limit", "1000")
	h.Set("anthropic-ratelimit-requests-remaining", "50")
	h.Set("anthropic-ratelimit-tokens-limit", "80000")
	h.Set("anthropic-ratelimit-tokens-remaining", "78000")
	h.Set("anthropic-ratelimit-requests-reset", "8h0m0s")
	h.Set("anthropic-ratelimit-tokens-reset", "24h0m0s")

	info := parseRateLimitHeaders(h)

	if info.LimitRequests != 1000 {
		t.Errorf("LimitRequests: got %d, want 1000", info.LimitRequests)
	}
	if info.RemainingRequests != 50 {
		t.Errorf("RemainingRequests: got %d, want 50", info.RemainingRequests)
	}
	if info.LimitTokens != 80000 {
		t.Errorf("LimitTokens: got %d, want 80000", info.LimitTokens)
	}
	if info.RemainingTokens != 78000 {
		t.Errorf("RemainingTokens: got %d, want 78000", info.RemainingTokens)
	}
	if info.ResetRequests != 8*time.Hour {
		t.Errorf("ResetRequests: got %v, want 8h", info.ResetRequests)
	}
}

func TestParseRateLimitHeaders_AnthropicNewPrefix(t *testing.T) {
	// Newer Anthropic API uses "reratelimit-" prefix.
	h := http.Header{}
	h.Set("reratelimit-requests-limit", "2000")
	h.Set("reratelimit-requests-remaining", "1999")
	h.Set("reratelimit-tokens-limit", "200000")
	h.Set("reratelimit-tokens-remaining", "198000")

	info := parseRateLimitHeaders(h)

	if info.LimitRequests != 2000 {
		t.Errorf("LimitRequests: got %d, want 2000", info.LimitRequests)
	}
	if info.RemainingRequests != 1999 {
		t.Errorf("RemainingRequests: got %d, want 1999", info.RemainingRequests)
	}
	if info.LimitTokens != 200000 {
		t.Errorf("LimitTokens: got %d, want 200000", info.LimitTokens)
	}
}

func TestParseRateLimitHeaders_Empty(t *testing.T) {
	h := http.Header{}
	info := parseRateLimitHeaders(h)
	if !info.IsEmpty() {
		t.Error("expected IsEmpty for headers without rate-limit info")
	}
	if info.RemainingRequests != -1 {
		t.Errorf("expected -1 for unknown RemainingRequests, got %d", info.RemainingRequests)
	}
}

func TestParseRateLimitHeaders_FloatValue(t *testing.T) {
	// Some providers return floats like "100.0".
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "100.0")
	h.Set("x-ratelimit-limit-requests", "500.0")

	info := parseRateLimitHeaders(h)
	if info.RemainingRequests != 100 {
		t.Errorf("RemainingRequests: got %d, want 100", info.RemainingRequests)
	}
	if info.LimitRequests != 500 {
		t.Errorf("LimitRequests: got %d, want 500", info.LimitRequests)
	}
}

func TestRateLimitInfo_Fractions(t *testing.T) {
	tests := []struct {
		name     string
		info     RateLimitInfo
		reqFrac  float64
		tokFrac  float64
		critical bool
	}{
		{
			name:     "empty defaults to 1.0",
			info:     RateLimitInfo{RemainingRequests: -1, RemainingTokens: -1, LimitRequests: -1, LimitTokens: -1},
			reqFrac:  1.0,
			tokFrac:  1.0,
			critical: false,
		},
		{
			name:     "half remaining",
			info:     RateLimitInfo{RemainingRequests: 500, LimitRequests: 1000, RemainingTokens: 40000, LimitTokens: 80000},
			reqFrac:  0.5,
			tokFrac:  0.5,
			critical: false,
		},
		{
			name:     "critical low",
			info:     RateLimitInfo{RemainingRequests: 5, LimitRequests: 1000, RemainingTokens: 8000, LimitTokens: 80000},
			reqFrac:  0.005,
			tokFrac:  0.1,
			critical: true,
		},
		{
			name:     "zero remaining",
			info:     RateLimitInfo{RemainingRequests: 0, LimitRequests: 100, RemainingTokens: 0, LimitTokens: 1000},
			reqFrac:  0.0,
			tokFrac:  0.0,
			critical: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.RequestFractionRemaining(); got != tt.reqFrac {
				t.Errorf("RequestFractionRemaining: got %f, want %f", got, tt.reqFrac)
			}
			if got := tt.info.TokenFractionRemaining(); got != tt.tokFrac {
				t.Errorf("TokenFractionRemaining: got %f, want %f", got, tt.tokFrac)
			}
			if got := tt.info.IsCritical(); got != tt.critical {
				t.Errorf("IsCritical: got %v, want %v", got, tt.critical)
			}
		})
	}
}

func TestRateLimitTracker_Update(t *testing.T) {
	tracker := newRateLimitTracker()

	// Initially empty
	info := tracker.Snapshot()
	if !info.IsEmpty() {
		t.Error("expected empty initially")
	}

	// Update with OpenAI headers
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "100")
	h.Set("x-ratelimit-limit-requests", "1000")
	tracker.Update(h)

	info = tracker.Snapshot()
	if info.RemainingRequests != 100 {
		t.Errorf("RemainingRequests: got %d, want 100", info.RemainingRequests)
	}
	if info.LimitRequests != 1000 {
		t.Errorf("LimitRequests: got %d, want 1000", info.LimitRequests)
	}
	if info.CapturedAt.IsZero() {
		t.Error("CapturedAt should be set")
	}
}

func TestRateLimitTracker_UpdateEmptyIgnored(t *testing.T) {
	tracker := newRateLimitTracker()

	// Set valid data first
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "50")
	tracker.Update(h)
	if tracker.Snapshot().RemainingRequests != 50 {
		t.Fatal("expected 50 after first update")
	}

	// Update with empty headers should be ignored (preserve previous)
	tracker.Update(http.Header{})
	if tracker.Snapshot().RemainingRequests != 50 {
		t.Error("expected 50 preserved after empty update")
	}
}

func TestParseDurationSafe(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"120ms", 120 * time.Millisecond},
		{"6h0m0s", 6 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"86400000", 86400 * time.Second}, // raw ms
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDurationSafe(tt.input)
			if got != tt.want {
				t.Errorf("parseDurationSafe(%q): got %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestRateLimitTracker_Concurrent verifies thread-safe access.
func TestRateLimitTracker_Concurrent(t *testing.T) {
	tracker := newRateLimitTracker()
	done := make(chan struct{})

	// Writer goroutine
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			h := http.Header{}
			h.Set("x-ratelimit-remaining-requests", "100")
			tracker.Update(h)
		}
	}()

	// Reader goroutines
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = tracker.Snapshot()
			}
		}()
	}

	<-done
	// If we get here without deadlock or panic, the test passes.
}
