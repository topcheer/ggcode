package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
)

// #3295: sub-agent metric events must carry their own model attribution.
// stampMetricModel is the origin of that attribution - collectors
// downstream only fill-if-empty, so a missing stamp here silently
// attributes every sub-agent's tokens to the parent session's model.

// namedMockProvider embeds the package mockProvider (which satisfies
// provider.Provider) and adds ModelNameProvider with an injectable name.
type namedMockProvider struct {
	mockProvider
	name string
}

func (p *namedMockProvider) ModelName() string { return p.name }

func TestIssue3295StampMetricModelFillsFromProvider(t *testing.T) {
	ev := metrics.MetricEvent{Type: "llm"}
	stampMetricModel(&namedMockProvider{name: "glm-4.5-air"}, &ev)
	if ev.Model != "glm-4.5-air" {
		t.Fatalf("Model = %q, want provider's model name", ev.Model)
	}
}

func TestIssue3295StampMetricModelSkipsBlankName(t *testing.T) {
	// A provider reporting an empty name must not blank out an
	// already-stamped event nor write an empty attribution.
	ev := metrics.MetricEvent{Type: "llm", Model: "preexisting"}
	stampMetricModel(&namedMockProvider{name: ""}, &ev)
	if ev.Model != "preexisting" {
		t.Fatalf("Model = %q, want preexisting stamp preserved on blank provider name", ev.Model)
	}
}

func TestIssue3295StampMetricModelNoopForNonReportingProvider(t *testing.T) {
	// Plain mockProvider does not implement ModelNameProvider: the
	// collector's fill-if-empty path remains the sole source.
	ev := metrics.MetricEvent{Type: "llm", Model: "parent-session-model"}
	stampMetricModel(&mockProvider{}, &ev)
	if ev.Model != "parent-session-model" {
		t.Fatalf("Model = %q, want untouched for non-ModelNameProvider", ev.Model)
	}
}
