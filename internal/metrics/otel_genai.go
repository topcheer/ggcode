package metrics

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OTLP/JSON export conforming to the OpenTelemetry GenAI semantic conventions
// (https://github.com/open-telemetry/semantic-conventions-genai). This is the
// 2025-2026 industry standard for AI agent observability: every LLM call and
// tool execution becomes a span with gen_ai.* attributes that standard OTLP
// backends (Jaeger, Tempo, Grafana, Langfuse, Datadog, Honeycomb) ingest
// without a custom converter.
//
// Span layout:
//
//	invoke_agent ggcode            (root, INTERNAL)   gen_ai.operation.name="invoke_agent"
//	└── turn N                     (INTERNAL)
//	    ├── chat <model>           (CLIENT)           gen_ai.usage.*, time_to_first_chunk
//	    └── execute_tool <name>    (INTERNAL)         gen_ai.tool.name, error.type
//
// The output is an OTLP/JSON ExportTraceServiceRequest. No message/tool content
// is captured (Opt-In per spec) — only metadata, timings, and token counts.

const (
	otlpKindInternal = 1 // SPAN_KIND_INTERNAL
	otlpKindClient   = 3 // SPAN_KIND_CLIENT

	otlpStatusError = 2 // STATUS_CODE_ERROR
)

// --- OTLP JSON structures (proto3 JSON mapping; 64-bit ints as strings) ---

type otlpExportRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpAttr `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttr      `json:"attributes,omitempty"`
	Status            *otlpSpanStatus `json:"status,omitempty"`
}

type otlpSpanStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpAttr struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

// --- attribute constructors ---

func strAttr(k, v string) otlpAttr {
	return otlpAttr{Key: k, Value: otlpValue{StringValue: &v}}
}

func intAttr(k string, v int64) otlpAttr {
	s := strconv.FormatInt(v, 10)
	return otlpAttr{Key: k, Value: otlpValue{IntValue: &s}}
}

func boolAttr(k string, v bool) otlpAttr {
	return otlpAttr{Key: k, Value: otlpValue{BoolValue: &v}}
}

func dblAttr(k string, v float64) otlpAttr {
	return otlpAttr{Key: k, Value: otlpValue{DoubleValue: &v}}
}

// --- ID generation (deterministic, same inputs -> same trace) ---

// otlpTraceID derives a 16-byte (32 hex char) trace ID from the session ID.
func otlpTraceID(sessionID string) string {
	h := sha1.Sum([]byte("ggcode-trace:" + sessionID))
	return hex.EncodeToString(h[:16])
}

// normalizeProviderName converts a ggcode vendor name into the lowercase
// dotted form used by gen_ai.provider.name (e.g. "GitHub Copilot" ->
// "github.copilot").
func normalizeProviderName(vendor string) string {
	p := strings.ToLower(strings.TrimSpace(vendor))
	p = strings.ReplaceAll(p, " ", ".")
	p = strings.ReplaceAll(p, "_", ".")
	return p
}

// eventEnd returns the completion time of an event.
func eventEnd(ev MetricEvent) time.Time { return ev.Timestamp }

// --- event -> span conversion ---

func otlpSpanFromLLM(ev MetricEvent, model, provider string) otlpSpan {
	name := "chat"
	attrs := []otlpAttr{
		strAttr("gen_ai.operation.name", "chat"),
	}
	if provider != "" {
		attrs = append(attrs, strAttr("gen_ai.provider.name", provider))
	}
	if model != "" {
		attrs = append(attrs, strAttr("gen_ai.request.model", model))
		name = "chat " + model
	}
	attrs = append(attrs, boolAttr("gen_ai.request.stream", true))
	if ev.InputTokens > 0 {
		attrs = append(attrs, intAttr("gen_ai.usage.input_tokens", int64(ev.InputTokens)))
	}
	if ev.OutputTokens > 0 {
		attrs = append(attrs, intAttr("gen_ai.usage.output_tokens", int64(ev.OutputTokens)))
	}
	if ev.CacheRead > 0 {
		attrs = append(attrs, intAttr("gen_ai.usage.cache_read.input_tokens", int64(ev.CacheRead)))
	}
	if ev.CacheWrite > 0 {
		attrs = append(attrs, intAttr("gen_ai.usage.cache_write.input_tokens", int64(ev.CacheWrite)))
	}
	if ev.TTFT > 0 {
		attrs = append(attrs, dblAttr("gen_ai.response.time_to_first_chunk", ev.TTFT.Seconds()))
	}
	return otlpSpan{
		SpanID:            spanID("llm", strconv.Itoa(ev.TurnIndex), ev.Timestamp.String()),
		Name:              name,
		Kind:              otlpKindClient,
		StartTimeUnixNano: strconv.FormatInt(eventStart(ev).UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(eventEnd(ev).UnixNano(), 10),
		Attributes:        attrs,
	}
}

func otlpSpanFromTool(ev MetricEvent) otlpSpan {
	toolName := ev.ToolName
	if toolName == "" {
		toolName = "unknown"
	}
	s := otlpSpan{
		SpanID:            spanID("tool", strconv.Itoa(ev.TurnIndex), ev.Timestamp.String()),
		Name:              "execute_tool " + toolName,
		Kind:              otlpKindInternal,
		StartTimeUnixNano: strconv.FormatInt(eventStart(ev).UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(eventEnd(ev).UnixNano(), 10),
		Attributes: []otlpAttr{
			strAttr("gen_ai.operation.name", "execute_tool"),
			strAttr("gen_ai.tool.name", toolName),
		},
	}
	if !ev.ToolSuccess {
		msg := ev.ToolError
		if msg == "" {
			msg = "tool execution failed"
		}
		s.Status = &otlpSpanStatus{Code: otlpStatusError, Message: msg}
		s.Attributes = append(s.Attributes, strAttr("error.type", "tool_execution_error"))
	}
	return s
}

// ExportOTLPTrace converts raw metric events into an OTLP/JSON
// ExportTraceServiceRequest following the OpenTelemetry GenAI semantic
// conventions. The returned document can be POSTed to an OTLP/HTTP endpoint or
// imported by OTel-compatible backends.
func ExportOTLPTrace(sessionID, vendor, endpoint, model string, createdAt time.Time, events []MetricEvent) ([]byte, error) {
	provider := normalizeProviderName(vendor)
	traceID := otlpTraceID(sessionID)
	rootSpanID := spanID(sessionID)

	spans := make([]otlpSpan, 0, len(events)+2)

	// Group events by turn, preserving chronological order (same as BuildSpanTree).
	turnEvents := make(map[int][]MetricEvent)
	turnOrder := make([]int, 0)
	for _, ev := range events {
		if _, exists := turnEvents[ev.TurnIndex]; !exists {
			turnOrder = append(turnOrder, ev.TurnIndex)
		}
		turnEvents[ev.TurnIndex] = append(turnEvents[ev.TurnIndex], ev)
	}
	sort.Ints(turnOrder)

	rootStart := createdAt
	if rootStart.IsZero() && len(events) > 0 {
		rootStart = eventStart(events[0])
	}
	rootEnd := createdAt
	for _, ev := range events {
		if end := eventEnd(ev); end.After(rootEnd) {
			rootEnd = end
		}
	}

	// Root agent span.
	rootAttrs := []otlpAttr{strAttr("gen_ai.operation.name", "invoke_agent")}
	if provider != "" {
		rootAttrs = append(rootAttrs, strAttr("gen_ai.provider.name", provider))
	}
	if model != "" {
		rootAttrs = append(rootAttrs, strAttr("gen_ai.request.model", model))
	}
	rootAttrs = append(rootAttrs, strAttr("gen_ai.conversation.id", sessionID))
	spans = append(spans, otlpSpan{
		TraceID:           traceID,
		SpanID:            rootSpanID,
		Name:              "invoke_agent ggcode",
		Kind:              otlpKindInternal,
		StartTimeUnixNano: strconv.FormatInt(rootStart.UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(rootEnd.UnixNano(), 10),
		Attributes:        rootAttrs,
	})

	for _, turn := range turnOrder {
		evs := turnEvents[turn]
		turnSpan := otlpSpan{
			TraceID:      traceID,
			SpanID:       spanID("turn", sessionID, strconv.Itoa(turn)),
			ParentSpanID: rootSpanID,
			Name:         fmt.Sprintf("turn %d", turn),
			Kind:         otlpKindInternal,
		}
		// Compute the turn's time bounds from its children, then emit the
		// turn span before its children (parent-before-child ordering).
		turnStart := eventStart(evs[0])
		turnEnd := eventEnd(evs[0])
		children := make([]otlpSpan, 0, len(evs))
		for _, ev := range evs {
			var child otlpSpan
			switch ev.Type {
			case "llm":
				child = otlpSpanFromLLM(ev, model, provider)
			case "tool":
				child = otlpSpanFromTool(ev)
			default:
				continue
			}
			child.TraceID = traceID
			child.ParentSpanID = turnSpan.SpanID
			if s := eventStart(ev); s.Before(turnStart) {
				turnStart = s
			}
			if end := eventEnd(ev); end.After(turnEnd) {
				turnEnd = end
			}
			children = append(children, child)
		}
		turnSpan.StartTimeUnixNano = strconv.FormatInt(turnStart.UnixNano(), 10)
		turnSpan.EndTimeUnixNano = strconv.FormatInt(turnEnd.UnixNano(), 10)
		spans = append(spans, turnSpan)
		spans = append(spans, children...)
	}

	req := otlpExportRequest{
		ResourceSpans: []otlpResourceSpans{{
			Resource: otlpResource{Attributes: []otlpAttr{
				strAttr("service.name", "ggcode"),
			}},
			ScopeSpans: []otlpScopeSpans{{
				Scope: otlpScope{Name: "github.com/topcheer/ggcode/internal/metrics"},
				Spans: spans,
			}},
		}},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal OTLP trace: %w", err)
	}
	return data, nil
}
