package metrics

import (
	"encoding/json"
	"time"
)

// TraceDocument is the top-level JSON structure exported by ExportTrace.
// It captures the complete execution trace of a session: every LLM API call,
// every tool invocation, aggregated summaries, and per-turn breakdowns.
//
// The format is designed to be compatible with common observability pipelines:
// the "events" array follows a flat, append-only event log pattern (similar to
// OpenTelemetry span events), while "summary" and "turns" provide pre-aggregated
// views for quick analysis without replaying events.
type TraceDocument struct {
	SessionID  string         `json:"session_id"`
	Vendor     string         `json:"vendor,omitempty"`
	Endpoint   string         `json:"endpoint,omitempty"`
	Model      string         `json:"model,omitempty"`
	CreatedAt  time.Time      `json:"created_at,omitempty"`
	ExportedAt time.Time      `json:"exported_at"`
	Summary    SessionSummary `json:"summary"`
	Events     []MetricEvent  `json:"events"`
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
	doc := TraceDocument{
		SessionID:  sessionID,
		Vendor:     vendor,
		Endpoint:   endpoint,
		Model:      model,
		CreatedAt:  createdAt,
		ExportedAt: time.Now(),
		Summary:    Summarize(events),
		Events:     events,
	}
	return json.MarshalIndent(doc, "", "  ")
}
