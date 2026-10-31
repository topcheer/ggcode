package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/session"
)

// #4619: recordMetric must attribute usage by the event's OWN vendor/endpoint
// (post-fill), never blanket-bill every event to the session (host) endpoint.
// Sub-agent events (Model stamped by stampMetricModel, Vendor/Endpoint empty)
// resolve to no bucket at all - keyed aggregation skips key=="" events,
// consistent with the endpoint_stats rebuild path - instead of inflating the
// host endpoint's /stats numbers.

func newIssue4619Bridge() *ChatBridge {
	return &ChatBridge{
		currentSes: &session.Session{
			ID:       "t4619",
			Vendor:   "zai",
			Endpoint: "https://parent",
			Model:    "glm-5",
		},
		usageTurnIndex: 1,
	}
}

func hostBucketKey() string {
	return session.EndpointStatsKey("zai", "https://parent")
}

func TestIssue4619SubAgentUsageNotBilledToHostEndpoint(t *testing.T) {
	b := newIssue4619Bridge()
	// Sub-agent event: Model stamped at emit time, Vendor/Endpoint empty.
	b.recordMetric(metrics.MetricEvent{Type: "llm", Model: "glm-4.5-air", InputTokens: 100})
	// The raw event stream still records it (per-model reporting intact)...
	if len(b.metricEvents) != 1 || b.metricEvents[0].Model != "glm-4.5-air" {
		t.Fatalf("metricEvents = %+v, want the sub-agent event with its own model", b.metricEvents)
	}
	if got := len(b.currentSes.EndpointMetrics); got != 0 {
		t.Fatalf("sub-agent usage landed in %d endpoint bucket(s); want 0 (must never inflate the host endpoint)", got)
	}
}

func TestIssue4619HostEventLandsInSessionBucket(t *testing.T) {
	b := newIssue4619Bridge()
	// Host event: no attribution at all - inherits the session triple.
	b.recordMetric(metrics.MetricEvent{Type: "llm", InputTokens: 50})
	bucket := b.currentSes.EndpointMetrics[hostBucketKey()]
	if len(bucket) != 1 {
		t.Fatalf("host bucket entries = %d, want 1 (attribution of host events is unchanged)", len(bucket))
	}
	if bucket[0].Vendor != "zai" || bucket[0].Endpoint != "https://parent" {
		t.Fatalf("host event attributed to (%q, %q), want session identity", bucket[0].Vendor, bucket[0].Endpoint)
	}
}

func TestIssue4619SelfAttributedEventLandsInOwnBucket(t *testing.T) {
	b := newIssue4619Bridge()
	// Event carrying its own full attribution (e.g. a future collector that
	// stamps vendor/endpoint at emit time) must land in ITS OWN bucket, not
	// the host session's.
	b.recordMetric(metrics.MetricEvent{Type: "llm", Model: "glm-4.5-air", Vendor: "openai", Endpoint: "https://sub", InputTokens: 7})
	ownKey := session.EndpointStatsKey("openai", "https://sub")
	if got := len(b.currentSes.EndpointMetrics[ownKey]); got != 1 {
		t.Fatalf("self-attributed bucket entries = %d, want 1", got)
	}
	if got := len(b.currentSes.EndpointMetrics[hostBucketKey()]); got != 0 {
		t.Fatalf("self-attributed event also landed in host bucket (%d entries), want 0", got)
	}
}
