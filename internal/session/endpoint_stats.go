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

func (s *Session) ensureEndpointStats() {
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
	s.EndpointUsage = make(map[string]provider.TokenUsage)
	s.EndpointMetrics = make(map[string][]metrics.MetricEvent)
	for _, entry := range s.UsageHistory {
		key := EndpointStatsKey(entry.Vendor, entry.Endpoint)
		if key == "" {
			continue
		}
		current := s.EndpointUsage[key]
		current.Add(entry.Usage)
		s.EndpointUsage[key] = current
	}
	for _, ev := range s.Metrics {
		key := EndpointStatsKey(ev.Vendor, ev.Endpoint)
		if key == "" {
			continue
		}
		s.EndpointMetrics[key] = append(s.EndpointMetrics[key], ev)
	}
}

func (s *Session) AddUsageForEndpoint(vendor, endpoint string, usage provider.TokenUsage) {
	if s == nil {
		return
	}
	key := EndpointStatsKey(vendor, endpoint)
	if key == "" {
		return
	}
	s.ensureEndpointStats()
	current := s.EndpointUsage[key]
	current.Add(usage)
	s.EndpointUsage[key] = current
}

func (s *Session) AppendMetricForEndpoint(vendor, endpoint string, ev metrics.MetricEvent) {
	if s == nil {
		return
	}
	key := EndpointStatsKey(vendor, endpoint)
	if key == "" {
		return
	}
	s.ensureEndpointStats()
	s.EndpointMetrics[key] = append(s.EndpointMetrics[key], ev)
}

func (s *Session) UsageForEndpoint(vendor, endpoint string) provider.TokenUsage {
	if s == nil {
		return provider.TokenUsage{}
	}
	key := EndpointStatsKey(vendor, endpoint)
	if key == "" {
		if len(s.EndpointUsage) == 0 && len(s.UsageHistory) == 0 {
			return s.TokenUsage
		}
		return provider.TokenUsage{}
	}
	if s.EndpointUsage != nil {
		if usage, ok := s.EndpointUsage[key]; ok {
			return usage
		}
	}
	var rebuilt provider.TokenUsage
	found := false
	for _, entry := range s.UsageHistory {
		if EndpointStatsKey(entry.Vendor, entry.Endpoint) != key {
			continue
		}
		rebuilt.Add(entry.Usage)
		found = true
	}
	if found {
		return rebuilt
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
	if key == "" {
		if len(s.EndpointMetrics) == 0 {
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
	if s.EndpointMetrics != nil {
		if events, ok := s.EndpointMetrics[key]; ok {
			return append([]metrics.MetricEvent(nil), events...)
		}
	}
	filtered := make([]metrics.MetricEvent, 0)
	hasMetadata := false
	for _, ev := range s.Metrics {
		if strings.TrimSpace(ev.Vendor) != "" || strings.TrimSpace(ev.Endpoint) != "" {
			hasMetadata = true
		}
		if EndpointStatsKey(ev.Vendor, ev.Endpoint) == key {
			filtered = append(filtered, ev)
		}
	}
	if len(filtered) > 0 || hasMetadata {
		return filtered
	}
	sessionKey := EndpointStatsKey(s.Vendor, s.Endpoint)
	if sessionKey == key || sessionKey == "" {
		return append([]metrics.MetricEvent(nil), s.Metrics...)
	}
	return nil
}
