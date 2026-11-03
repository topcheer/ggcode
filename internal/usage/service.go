package usage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	cacheTTL    = 3 * time.Minute
	negativeTTL = time.Minute
	httpTimeout = 10 * time.Second
)

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

func (s *Service) Register(p Probe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes[p.Vendor()] = p
}

func (s *Service) Has(vendor string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.probes[vendor]
	return ok
}

func (s *Service) Resolve(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.probes {
		if m, ok := p.(URLMatcher); ok && m.MatchesURL(baseURL) {
			return id
		}
	}
	return ""
}

func (s *Service) Get(ctx context.Context, vendor, baseURL, apiKey string) (*UsageInfo, error) {
	for {
		s.mu.Lock()
		p, ok := s.probes[vendor]
		if !ok {
			s.mu.Unlock()
			return nil, ErrUnsupported
		}
		if c, ok := s.cached[vendor]; ok {

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

		if err == nil || !errors.Is(err, context.Canceled) {

			var rl *RateLimitedError
			if errors.As(err, &rl) && rl.RetryAfter > 0 {
				d := rl.RetryAfter
				if d < time.Second {
					d = time.Second
				}
				if d > 10*time.Minute {
					d = 10 * time.Minute
				}

				res.at = time.Now().Add(d - negativeTTL)
			}
			s.cached[vendor] = res
		}
		delete(s.inflight, vendor)
		s.mu.Unlock()

		call.res = res
		close(call.done)
		return info, err
	}
}

func (s *Service) Invalidate(vendor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cached, vendor)
}

func (s *Service) RefreshInvalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for v, c := range s.cached {
		if c.err == nil {
			delete(s.cached, v)
		}
	}
}
