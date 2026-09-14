package usage

import (
	"context"
	"errors"
	"sync"
	"time"
)

// cacheTTL/negativeTTL follow the sub2api pattern: success is cheap to
// reuse, failure must not hammer a down endpoint.
const (
	cacheTTL    = 3 * time.Minute
	negativeTTL = time.Minute
	httpTimeout = 10 * time.Second
)

// Service caches probe results per vendor and merges concurrent queries
// for the same vendor (singleflight by mutex; probes are rare and
// side-effect free, so a simple per-vendor in-flight map suffices).
type Service struct {
	mu       sync.Mutex
	probes   map[string]Probe
	cached   map[string]cachedResult
	inflight map[string]*inflightCall
}

type cachedResult struct {
	info *UsageInfo
	err  error
	at   time.Time
}

type inflightCall struct {
	done chan struct{}
	res  cachedResult
}

func NewService() *Service {
	return &Service{
		probes:   map[string]Probe{},
		cached:   map[string]cachedResult{},
		inflight: map[string]*inflightCall{},
	}
}

// Register adds a probe (idempotent per vendor name).
func (s *Service) Register(p Probe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes[p.Vendor()] = p
}

// Has reports whether a probe is registered for the vendor (batch 2:
// the TUI lists only vendors it can actually query).
func (s *Service) Has(vendor string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.probes[vendor]
	return ok
}

// Get returns the cached usage for a vendor, or queries it. Errors are
// cached briefly (negative cache) so a failing endpoint does not get
// hammered by sidebar refreshes.
func (s *Service) Get(ctx context.Context, vendor, baseURL, apiKey string) (*UsageInfo, error) {
	for {
		s.mu.Lock()
		p, ok := s.probes[vendor]
		if !ok {
			s.mu.Unlock()
			return nil, ErrUnsupported
		}
		if c, ok := s.cached[vendor]; ok {
			// #2353-②: only SUCCESS is cached for cacheTTL. The old check
			// (`fresh := < cacheTTL`) absorbed negative results for 3min,
			// making the negativeTTL branch dead code and tripling the
			// error cache window.
			fresh := c.err == nil && time.Since(c.at) < cacheTTL
			negFresh := c.err != nil && time.Since(c.at) < negativeTTL
			if fresh || negFresh {
				s.mu.Unlock()
				return c.info, c.err
			}
		}
		if call, ok := s.inflight[vendor]; ok {
			s.mu.Unlock()
			select {
			case <-call.done:
				return call.res.info, call.res.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		call := &inflightCall{done: make(chan struct{})}
		s.inflight[vendor] = call
		s.mu.Unlock()

		qctx, cancel := context.WithTimeout(ctx, httpTimeout)
		info, err := p.Fetch(qctx, baseURL, apiKey)
		cancel()
		res := cachedResult{info: info, err: err, at: time.Now()}

		s.mu.Lock()
		// #2353-③: a caller-initiated cancellation must NOT poison the
		// negative cache for other callers (the TUI refresh path cancels
		// freely). Canceled propagates from the PARENT ctx; the probe's own
		// httpTimeout surfaces as DeadlineExceeded and stays cacheable.
		if err == nil || !errors.Is(err, context.Canceled) {
			// #2150 batch 2b blind-spot linkage: a 429 sizes the negative
			// cache to the server's Retry-After (clamped 1s..10min) instead
			// of the default negativeTTL - the old behavior re-probed a
			// rate-limited endpoint every minute, exactly the hammering the
			// negative cache exists to prevent.
			var rl *RateLimitedError
			if errors.As(err, &rl) && rl.RetryAfter > 0 {
				d := rl.RetryAfter
				if d < time.Second {
					d = time.Second
				}
				if d > 10*time.Minute {
					d = 10 * time.Minute
				}
				// Forward-date the entry so negativeTTL expiry (since(at) <
				// negativeTTL) lands exactly when Retry-After elapses.
				res.at = time.Now().Add(d - negativeTTL)
			}
			s.cached[vendor] = res
		}
		delete(s.inflight, vendor)
		s.mu.Unlock()
		// #2353-①: publish the result BEFORE closing done - waiters wake
		// from close and read call.res; the old order let a waiter observe
		// the zero value (nil, nil), a nil-deref for callers and a data
		// race under -race.
		call.res = res
		close(call.done)
		return info, err
	}
}

// Invalidate drops the cache for one vendor (used by the error-path
// hook: a 429 means the cached numbers are stale NOW).
func (s *Service) Invalidate(vendor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cached, vendor)
}
