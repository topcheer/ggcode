package usage

import (
	"context"
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
			fresh := time.Since(c.at) < cacheTTL
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
		s.cached[vendor] = res
		delete(s.inflight, vendor)
		s.mu.Unlock()
		close(call.done)
		call.res = res
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
