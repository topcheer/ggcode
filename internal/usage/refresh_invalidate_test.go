package usage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRefreshInvalidateKeepsNegativeEntries(t *testing.T) {
	p := &fakeProbe{vendor: "ri-vendor"}
	svc := NewService()
	svc.Register(p)

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

func TestRefreshKeepsServiceUnderRepeatedPresses(t *testing.T) {
	svc := NewService()
	p := &fakeProbe{vendor: "k"}
	svc.Register(p)

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

var minuteUnit = time.Minute
