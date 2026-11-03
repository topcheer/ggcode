package session

import (
	"strings"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

const maxEndpointMetricsPerKey = 200

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

func (s *Session) AddUsageHistoryEntry(entry UsageEntry) {
	if s == nil {
		return
	}
	s.endpointStatsMu.Lock()
	s.UsageHistory = append(s.UsageHistory, entry)
	s.endpointStatsMu.Unlock()
}

func (s *Session) AppendMetricEvent(ev metrics.MetricEvent) {
	if s == nil {
		return
	}
	s.endpointStatsMu.Lock()
	s.Metrics = append(s.Metrics, ev)
	s.endpointStatsMu.Unlock()
}

func (s *Session) UsageHistorySnapshot() []UsageEntry {
	return s.usageHistorySnapshot()
}

func (s *Session) usageHistorySnapshot() []UsageEntry {
	s.endpointStatsMu.RLock()
	snap := append([]UsageEntry(nil), s.UsageHistory...)
	s.endpointStatsMu.RUnlock()
	return snap
}

func (s *Session) MetricsSnapshot() []metrics.MetricEvent {
	return s.metricsSnapshot()
}

func (s *Session) metricsSnapshot() []metrics.MetricEvent {
	s.endpointStatsMu.RLock()
	snap := append([]metrics.MetricEvent(nil), s.Metrics...)
	s.endpointStatsMu.RUnlock()
	return snap
}

func (s *Session) RebuildEndpointStats() {
	if s == nil {
		return
	}
	usageByEndpoint := make(map[string]provider.TokenUsage)
	metricsByEndpoint := make(map[string][]metrics.MetricEvent)
	usageHistory := s.usageHistorySnapshot()
	for _, entry := range usageHistory {
		key := EndpointStatsKey(entry.Vendor, entry.Endpoint)
		if key == "" {
			continue
		}
		usageByEndpoint[key] = usageByEndpoint[key].Add(entry.Usage)
	}
	for _, ev := range s.metricsSnapshot() {
		key := EndpointStatsKey(ev.Vendor, ev.Endpoint)
		if key == "" {
			continue
		}
		metricsByEndpoint[key] = append(metricsByEndpoint[key], ev)

		if len(metricsByEndpoint[key]) > maxEndpointMetricsPerKey {
			excess := len(metricsByEndpoint[key]) - maxEndpointMetricsPerKey
			metricsByEndpoint[key] = metricsByEndpoint[key][excess:]
		}
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

	if len(s.EndpointMetrics[key]) > maxEndpointMetricsPerKey {
		excess := len(s.EndpointMetrics[key]) - maxEndpointMetricsPerKey
		s.EndpointMetrics[key] = s.EndpointMetrics[key][excess:]
	}
}

func (s *Session) UsageForEndpoint(vendor, endpoint string) provider.TokenUsage {
	if s == nil {
		return provider.TokenUsage{}
	}
	key := EndpointStatsKey(vendor, endpoint)
	s.endpointStatsMu.RLock()
	usage, ok := s.EndpointUsage[key]
	hasBuckets := len(s.EndpointUsage) > 0
	hasHistory := len(s.UsageHistory) > 0
	s.endpointStatsMu.RUnlock()
	if key == "" {
		if !hasBuckets && !hasHistory {
			return s.TokenUsage
		}
		return provider.TokenUsage{}
	}
	if ok {
		return usage
	}
	if !hasBuckets && hasHistory {
		s.RebuildEndpointStats()
		s.endpointStatsMu.RLock()
		usage, ok = s.EndpointUsage[key]
		s.endpointStatsMu.RUnlock()
		if ok {
			return usage
		}
	}

	sessionKey := EndpointStatsKey(s.Vendor, s.Endpoint)
	if !hasHistory && (sessionKey == key || sessionKey == "") {
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
	metricsSrc := append([]metrics.MetricEvent(nil), s.Metrics...)
	s.endpointStatsMu.RUnlock()
	if key == "" {
		if !hasBuckets {
			hasMetadata := false
			for _, ev := range metricsSrc {
				if strings.TrimSpace(ev.Vendor) != "" || strings.TrimSpace(ev.Endpoint) != "" {
					hasMetadata = true
					break
				}
			}
			if !hasMetadata {
				return metricsSrc
			}
		}
		return nil
	}
	if ok {
		return append([]metrics.MetricEvent(nil), events...)
	}
	if !hasBuckets && len(metricsSrc) > 0 {
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
	for _, ev := range metricsSrc {
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
		return metricsSrc
	}
	return nil
}
