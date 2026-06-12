package session

import (
	"strings"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

func EndpointStatsKey(vendor, endpoint string) string {
	vendor = strings.TrimSpace(vendor)
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case vendor == "":
		return endpoint
	case endpoint == "":
		return vendor
	default:
		return vendor + "/" + endpoint
	}
}

func (s *Session) ensureEndpointStatsLocked() {
	if s.EndpointUsage == nil {
		s.EndpointUsage = make(map[string]provider.TokenUsage)
	}
	if s.EndpointMetrics == nil {
		s.EndpointMetrics = make(map[string][]metrics.MetricEvent)
	}
}

func (s *Session) RebuildEndpointStats() {
	if s == nil {
		return
	}
	usageByEndpoint := make(map[string]provider.TokenUsage)
	metricsByEndpoint := make(map[string][]metrics.MetricEvent)
	for _, entry := range s.UsageHistory {
		key := EndpointStatsKey(entry.Vendor, entry.Endpoint)
		if key == "" {
			continue
		}
		usageByEndpoint[key] = usageByEndpoint[key].Add(entry.Usage)
	}
	for _, ev := range s.Metrics {
		key := EndpointStatsKey(ev.Vendor, ev.Endpoint)
		if key == "" {
			continue
		}
		metricsByEndpoint[key] = append(metricsByEndpoint[key], ev)
	}
	s.endpointStatsMu.Lock()
	s.EndpointUsage = usageByEndpoint
	s.EndpointMetrics = metricsByEndpoint
	s.endpointStatsMu.Unlock()
}

func (s *Session) AddUsageForEndpoint(vendor, endpoint string, usage provider.TokenUsage) {
	if s == nil {
		return
	}
	key := EndpointStatsKey(vendor, endpoint)
	if key == "" {
		return
	}
	s.endpointStatsMu.Lock()
	defer s.endpointStatsMu.Unlock()
	s.ensureEndpointStatsLocked()
	s.EndpointUsage[key] = s.EndpointUsage[key].Add(usage)
}

func (s *Session) AppendMetricForEndpoint(vendor, endpoint string, ev metrics.MetricEvent) {
	if s == nil {
		return
	}
	key := EndpointStatsKey(vendor, endpoint)
	if key == "" {
		return
	}
	s.endpointStatsMu.Lock()
	defer s.endpointStatsMu.Unlock()
	s.ensureEndpointStatsLocked()
	s.EndpointMetrics[key] = append(s.EndpointMetrics[key], ev)
}

func (s *Session) UsageForEndpoint(vendor, endpoint string) provider.TokenUsage {
	if s == nil {
		return provider.TokenUsage{}
	}
	key := EndpointStatsKey(vendor, endpoint)
	s.endpointStatsMu.RLock()
	usage, ok := s.EndpointUsage[key]
	hasBuckets := len(s.EndpointUsage) > 0
	s.endpointStatsMu.RUnlock()
	if key == "" {
		if !hasBuckets && len(s.UsageHistory) == 0 {
			return s.TokenUsage
		}
		return provider.TokenUsage{}
	}
	if ok {
		return usage
	}
	if !hasBuckets && len(s.UsageHistory) > 0 {
		s.RebuildEndpointStats()
		s.endpointStatsMu.RLock()
		usage, ok = s.EndpointUsage[key]
		s.endpointStatsMu.RUnlock()
		if ok {
			return usage
		}
	}
	sessionKey := EndpointStatsKey(s.Vendor, s.Endpoint)
	if len(s.UsageHistory) == 0 && (sessionKey == key || sessionKey == "") {
		return s.TokenUsage
	}
	return provider.TokenUsage{}
}

func (s *Session) MetricsForEndpoint(vendor, endpoint string) []metrics.MetricEvent {
	if s == nil {
		return nil
	}
	key := EndpointStatsKey(vendor, endpoint)
	s.endpointStatsMu.RLock()
	events, ok := s.EndpointMetrics[key]
	hasBuckets := len(s.EndpointMetrics) > 0
	s.endpointStatsMu.RUnlock()
	if key == "" {
		if !hasBuckets {
			hasMetadata := false
			for _, ev := range s.Metrics {
				if strings.TrimSpace(ev.Vendor) != "" || strings.TrimSpace(ev.Endpoint) != "" {
					hasMetadata = true
					break
				}
			}
			if !hasMetadata {
				return append([]metrics.MetricEvent(nil), s.Metrics...)
			}
		}
		return nil
	}
	if ok {
		return append([]metrics.MetricEvent(nil), events...)
	}
	if !hasBuckets && len(s.Metrics) > 0 {
		s.RebuildEndpointStats()
		s.endpointStatsMu.RLock()
		events, ok = s.EndpointMetrics[key]
		hasBuckets = len(s.EndpointMetrics) > 0
		s.endpointStatsMu.RUnlock()
		if ok {
			return append([]metrics.MetricEvent(nil), events...)
		}
		if hasBuckets {
			return nil
		}
	}
	hasMetadata := false
	for _, ev := range s.Metrics {
		if strings.TrimSpace(ev.Vendor) != "" || strings.TrimSpace(ev.Endpoint) != "" {
			hasMetadata = true
			break
		}
	}
	if hasMetadata {
		return nil
	}
	sessionKey := EndpointStatsKey(s.Vendor, s.Endpoint)
	if sessionKey == key || sessionKey == "" {
		return append([]metrics.MetricEvent(nil), s.Metrics...)
	}
	return nil
}
