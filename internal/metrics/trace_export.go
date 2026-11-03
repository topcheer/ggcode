package metrics

import (
	"encoding/json"
	"time"
)

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
