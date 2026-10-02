package session

import (
	"strings"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

// maxEndpointMetricsPerKey caps the number of metric events stored per endpoint
// to prevent unbounded memory growth in long-running sessions.
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

// AddUsageHistoryEntry appends to UsageHistory under endpointStatsMu.
// #3086: cross-goroutine callback appends (`ses.UsageHistory = append(...)`
// in TUI/IM/desktop bridges) raced the endpoint-stats rebuild readers that
// range the same slice - `slice = append(slice, x)` updates len and pointer
// in separate steps, so a concurrent range can see a torn view or panic on
// the stale-array/new-len window. Appends and reads of the source slices
// now all pass through this mutex. Safe under higher-level session/bridge
// locks (the mutex is nested, never the other way around).
func (s *Session) AddUsageHistoryEntry(entry UsageEntry) {
	if s == nil {
		return
	}
	s.endpointStatsMu.Lock()
	s.UsageHistory = append(s.UsageHistory, entry)
	s.endpointStatsMu.Unlock()
}

// AppendMetricEvent appends to Metrics under endpointStatsMu - the
// Metrics-side twin of AddUsageHistoryEntry (#3086).
func (s *Session) AppendMetricEvent(ev metrics.MetricEvent) {
	if s == nil {
		return
	}
	s.endpointStatsMu.Lock()
	s.Metrics = append(s.Metrics, ev)
	s.endpointStatsMu.Unlock()
}

// UsageHistorySnapshot returns a copy of UsageHistory taken under the
// same lock the appends hold. Exported for TUI/desktop read paths that
// run off the session goroutine (cost snapshot per frame, /cost export)
// - #3086.
func (s *Session) UsageHistorySnapshot() []UsageEntry {
	return s.usageHistorySnapshot()
}

// usageHistorySnapshot returns a copy of UsageHistory taken under the
// same lock the appends hold (#3086).
func (s *Session) usageHistorySnapshot() []UsageEntry {
	s.endpointStatsMu.RLock()
	snap := append([]UsageEntry(nil), s.UsageHistory...)
	s.endpointStatsMu.RUnlock()
	return snap
}

// MetricsSnapshot returns a copy of Metrics taken under the same lock
// the appends hold. Exported for TUI read paths that capture the slice
// in closures crossing goroutines (/trace export) - #3086.
func (s *Session) MetricsSnapshot() []metrics.MetricEvent {
	return s.metricsSnapshot()
}

// metricsSnapshot returns a copy of Metrics taken under the same lock the
// appends hold (#3086).
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
	usageHistory := s.usageHistorySnapshot() // #3086: read under the append lock
	for _, entry := range usageHistory {
		key := EndpointStatsKey(entry.Vendor, entry.Endpoint)
		if key == "" {
			continue
		}
		usageByEndpoint[key] = usageByEndpoint[key].Add(entry.Usage)
	}
	for _, ev := range s.metricsSnapshot() { // #3086: read under the append lock
		key := EndpointStatsKey(ev.Vendor, ev.Endpoint)
		if key == "" {
			continue
		}
		metricsByEndpoint[key] = append(metricsByEndpoint[key], ev)
		// #800: apply the same 200-entry cap as AppendMetricForEndpoint --
		// the rebuild path previously restored unbounded history for long
		// sessions, violating the documented invariant.
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
	// Cap to prevent unbounded growth in long-running sessions.
	// Keep the most recent entries by dropping from the front.
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
	// #3086: hasHistory was read under the lock at the top of this
	// function; appends since then only shrink the empty-history window,
	// never reopen it.
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
	metricsSrc := append([]metrics.MetricEvent(nil), s.Metrics...) // #3086: read under the append lock
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
