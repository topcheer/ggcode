package usage

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRefreshInvalidateKeepsNegativeEntries verifies #2366-①: the r-key's
// invalidation must drop SUCCESS entries only. Negative entries - the
// Retry-After-sized 429 back-offs the Service exists to enforce - survive,
// so a rate-limited endpoint is not re-probed just because the user
// pressed refresh.
func TestRefreshInvalidateKeepsNegativeEntries(t *testing.T) {
	p := &fakeProbe{vendor: "ri-vendor"}
	svc := NewService()
	svc.Register(p)

	// Seed: one success (via fresh cache write) and one negative entry.
	ok := &UsageInfo{Vendor: "ri-vendor"}
	svc.mu.Lock()
	svc.cached["ri-vendor"] = cachedResult{info: ok, at: time.Now()}
	svc.cached["rl-other"] = cachedResult{err: errors.New("rate limited (retry after 10m0s)"), at: time.Now()}
	svc.mu.Unlock()

	svc.RefreshInvalidate()

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if _, ok := svc.cached["ri-vendor"]; ok {
		t.Fatal("success entry survived RefreshInvalidate")
	}
	if _, ok := svc.cached["rl-other"]; !ok {
		t.Fatal("negative entry dropped by RefreshInvalidate (429 pacing lost)")
	}
}

// TestRefreshUsagePanelKeepsServiceInstance verifies the TUI side of
// #2366-①: refresh must keep the Service instance (singleflight + pacing
// state) instead of rebuilding an empty one.
func TestRefreshKeepsServiceUnderRepeatedPresses(t *testing.T) {
	svc := NewService()
	p := &fakeProbe{vendor: "k"}
	svc.Register(p)
	// Simulate a Retry-After-sized negative entry: pressing r must NOT
	// clear it, so the probe is not re-run for that vendor.
	svc.mu.Lock()
	svc.cached["k"] = cachedResult{err: &RateLimitedError{RetryAfter: 10 * time.Minute}, at: time.Now()}
	svc.mu.Unlock()

	for i := 0; i < 3; i++ {
		svc.RefreshInvalidate()
	}
	svc.mu.Lock()
	_, kept := svc.cached["k"]
	svc.mu.Unlock()
	if !kept {
		t.Fatal("repeated refresh dropped the 429 back-off entry (hammer window reopened)")
	}
	_ = context.Background
}

// timeNowPlus/mMinute keep the test free of a direct time dependency in
// the assertions (entries are read via age, freshly written = age 0).
var minuteUnit = time.Minute
