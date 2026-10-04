package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/session"
)

// #3295: recordMetric must fill-if-empty, not blanket-overwrite. A sub-agent
// event already carrying its own model attribution keeps it; only events
// with no model inherit the parent session's identity.

func newIssue3295Bridge() *ChatBridge {
	return &ChatBridge{
		currentSes: &session.Session{
			ID:       "t",
			Vendor:   "zai",
			Endpoint: "https://parent",
			Model:    "glm-5",
		},
		usageTurnIndex: 3,
	}
}

func TestIssue3295SubAgentModelNotOverwritten(t *testing.T) {
	b := newIssue3295Bridge()
	b.recordMetric(metrics.MetricEvent{Type: "llm", Model: "glm-4.5-air", InputTokens: 100})
	if len(b.metricEvents) != 1 {
		t.Fatalf("metricEvents = %d, want 1", len(b.metricEvents))
	}
	if got := b.metricEvents[0].Model; got != "glm-4.5-air" {
		t.Fatalf("sub-agent event Model = %q, want its own attribution (was parent overwrite)", got)
	}
	if got := b.metricEvents[0].Vendor; got != "" {
		t.Fatalf("vendor on pre-stamped event = %q, want empty (never blanket-filled with parent identity)", got)
	}
}

func TestIssue3295UnstampedEventInheritsSessionModel(t *testing.T) {
	b := newIssue3295Bridge()
	b.recordMetric(metrics.MetricEvent{Type: "llm", InputTokens: 50})
	if got := b.metricEvents[0].Model; got != "glm-5" {
		t.Fatalf("unstamped event Model = %q, want session model glm-5", got)
	}
	if got := b.metricEvents[0].Endpoint; got != "https://parent" {
		t.Fatalf("unstamped event Endpoint = %q, want session endpoint", got)
	}
	if got := b.metricEvents[0].TurnIndex; got != 3 {
		t.Fatalf("TurnIndex = %d, want bridge usageTurnIndex", got)
	}
}
