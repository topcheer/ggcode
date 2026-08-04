package metrics

import (
	"encoding/json"
	"time"
)

// TraceDocument is the top-level JSON structure exported by ExportTrace.
// It captures the complete execution trace of a session: every LLM API call,
// every tool invocation, aggregated summaries, per-turn breakdowns, and a
// hierarchical span tree for observability tooling.
//
// The format provides three views of the same data:
//   - "events": flat, append-only event log (similar to OpenTelemetry span events)
//   - "span_tree": hierarchical session -> turn -> llm/tool spans (LangSmith/Langfuse format)
//   - "waterfall": per-turn latency analysis with parallel groups and critical path
//   - "summary" and "turns": pre-aggregated views for quick analysis
type TraceDocument struct {
	SessionID  string              `json:"session_id"`
	Vendor     string              `json:"vendor,omitempty"`
	Endpoint   string              `json:"endpoint,omitempty"`
	Model      string              `json:"model,omitempty"`
	CreatedAt  time.Time           `json:"created_at,omitempty"`
	ExportedAt time.Time           `json:"exported_at"`
	Summary    SessionSummary      `json:"summary"`
	Events     []MetricEvent       `json:"events"`
	SpanTree   SpanNode            `json:"span_tree"`
	Waterfall  []WaterfallAnalysis `json:"waterfall,omitempty"`
	ErrorSpans []SpanNode          `json:"error_spans,omitempty"`
}

// ExportTrace builds a TraceDocument from raw metric events and session metadata,
// then marshals it to indented JSON suitable for file export or piping to other
// tools (jq, observability platforms, etc.).
//
// Parameters:
//   - sessionID: the session identifier
//   - vendor, endpoint, model: provider metadata
//   - createdAt: session creation timestamp
//   - events: the raw metric events collected during the session
//
// Returns the JSON-encoded trace document. The caller is responsible for writing
// it to a file or other destination.
func ExportTrace(sessionID, vendor, endpoint, model string, createdAt time.Time, events []MetricEvent) ([]byte, error) {
	spanTree := BuildSpanTree(sessionID, events)
	doc := TraceDocument{
		SessionID:  sessionID,
		Vendor:     vendor,
		Endpoint:   endpoint,
		Model:      model,
		CreatedAt:  createdAt,
		ExportedAt: time.Now(),
		Summary:    Summarize(events),
		Events:     events,
		SpanTree:   spanTree,
		Waterfall:  AnalyzeWaterfall(events),
		ErrorSpans: FindErrorSpans(spanTree),
	}
	return json.MarshalIndent(doc, "", "  ")
}
