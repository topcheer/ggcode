package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/session"
)

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

	b.recordMetric(metrics.MetricEvent{Type: "llm", Model: "glm-4.5-air", InputTokens: 100})

	if len(b.metricEvents) != 1 || b.metricEvents[0].Model != "glm-4.5-air" {
		t.Fatalf("metricEvents = %+v, want the sub-agent event with its own model", b.metricEvents)
	}
	if got := len(b.currentSes.EndpointMetrics); got != 0 {
		t.Fatalf("sub-agent usage landed in %d endpoint bucket(s); want 0 (must never inflate the host endpoint)", got)
	}
}

func TestIssue4619HostEventLandsInSessionBucket(t *testing.T) {
	b := newIssue4619Bridge()

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

	b.recordMetric(metrics.MetricEvent{Type: "llm", Model: "glm-4.5-air", Vendor: "openai", Endpoint: "https://sub", InputTokens: 7})
	ownKey := session.EndpointStatsKey("openai", "https://sub")
	if got := len(b.currentSes.EndpointMetrics[ownKey]); got != 1 {
		t.Fatalf("self-attributed bucket entries = %d, want 1", got)
	}
	if got := len(b.currentSes.EndpointMetrics[hostBucketKey()]); got != 0 {
		t.Fatalf("self-attributed event also landed in host bucket (%d entries), want 0", got)
	}
}
