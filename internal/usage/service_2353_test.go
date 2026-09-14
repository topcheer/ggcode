package usage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProbe lets tests script probe results and observe call counts.
type fakeProbe struct {
	vendor string
	delay  time.Duration
	result atomic.Pointer[cachedResult]
	calls  atomic.Int32
}

func (f *fakeProbe) Vendor() string { return f.vendor }

func (f *fakeProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r := f.result.Load()
	if r == nil {
		return nil, errors.New("boom")
	}
	return r.info, r.err
}

// TestSingleflightWaiterGetsResult verifies #2353-①: a waiter that joins an
// in-flight call must observe the owner's result, never the zero value
// (nil, nil) that the close-before-publish order produced.
func TestSingleflightWaiterGetsResult(t *testing.T) {
	p := &fakeProbe{vendor: "waiter-vendor", delay: 50 * time.Millisecond}
	p.result.Store(&cachedResult{info: &UsageInfo{Vendor: "waiter-vendor"}})
	svc := NewService()
	svc.Register(p)

	var wg sync.WaitGroup
	type outcome struct {
		info *UsageInfo
		err  error
	}
	outcomes := make([]outcome, 8)
	for i := range outcomes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcomes[i].info, outcomes[i].err = svc.Get(context.Background(), "waiter-vendor", "https://x", "k")
		}(i)
	}
	wg.Wait()

	if n := p.calls.Load(); n != 1 {
		t.Fatalf("probe called %d times, want 1 (singleflight)", n)
	}
	for i, o := range outcomes {
		if o.err != nil {
			t.Fatalf("waiter %d got err: %v", i, o.err)
		}
		if o.info == nil || o.info.Vendor != "waiter-vendor" {
			t.Fatalf("waiter %d got (%v, %v) - zero value leaked from singleflight", i, o.info, o.err)
		}
	}
}

// TestNegativeCacheUsesShortTTL verifies #2353-②: an error is served from
// the negative cache for negativeTTL only. With cacheTTL == 3*negativeTTL
// this test would hang for minutes using the real constants, so it asserts
// the mechanism: after Invalidate-style expiry the probe is re-called, and
// while an error is fresh it is NOT re-probed.
func TestNegativeCacheUsesShortTTL(t *testing.T) {
	p := &fakeProbe{vendor: "neg-vendor"}
	p.result.Store(&cachedResult{err: errors.New("down")})
	svc := NewService()
	svc.Register(p)

	if _, err := svc.Get(context.Background(), "neg-vendor", "", ""); err == nil {
		t.Fatal("expected error from probe")
	}
	if _, err := svc.Get(context.Background(), "neg-vendor", "", ""); err == nil {
		t.Fatal("expected cached error")
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("error not negative-cached: %d probe calls", n)
	}

	// Simulate negativeTTL expiry: age the entry past negativeTTL but
	// well inside cacheTTL - the OLD code (fresh absorbs errors for 3min)
	// would still serve the stale error.
	svc.mu.Lock()
	c := svc.cached["neg-vendor"]
	c.at = time.Now().Add(-(negativeTTL + time.Second))
	svc.cached["neg-vendor"] = c
	svc.mu.Unlock()

	if _, err := svc.Get(context.Background(), "neg-vendor", "", ""); err == nil {
		t.Fatal("probe still failing, error expected")
	}
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("stale negative result served past negativeTTL (old fresh-bug): %d calls", n)
	}
}

// TestCancellationDoesNotPoisonCache verifies #2353-③: a caller cancelling
// its context mid-flight must not leave a negative cache entry that other
// callers then hit.
func TestCancellationDoesNotPoisonCache(t *testing.T) {
	p := &fakeProbe{vendor: "cancel-vendor", delay: 50 * time.Millisecond}
	p.result.Store(&cachedResult{info: &UsageInfo{Vendor: "cancel-vendor"}})
	svc := NewService()
	svc.Register(p)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	if _, err := svc.Get(ctx, "cancel-vendor", "", ""); err == nil {
		t.Fatal("expected cancellation error")
	}

	// A fresh caller must NOT inherit the cancellation as a cached error.
	info, err := svc.Get(context.Background(), "cancel-vendor", "", "")
	if err != nil {
		t.Fatalf("fresh caller got poisoned cache: %v", err)
	}
	if info == nil || info.Vendor != "cancel-vendor" {
		t.Fatalf("fresh caller got nil info")
	}
}
