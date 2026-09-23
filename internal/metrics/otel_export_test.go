package metrics

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func otelTestEvents() []MetricEvent {
	now := time.Now()
	return []MetricEvent{
		{Timestamp: now, TurnIndex: 0, Type: "llm", Model: "claude-x", Vendor: "anthropic",
			Duration: 2 * time.Second, TTFT: 300 * time.Millisecond,
			InputTokens: 100, OutputTokens: 50, CacheRead: 20},
		{Timestamp: now.Add(time.Second), TurnIndex: 0, Type: "tool",
			ToolName: "read_file", ToolSuccess: true, ToolDuration: 10 * time.Millisecond},
		{Timestamp: now.Add(2 * time.Second), TurnIndex: 1, Type: "tool",
			ToolName: "run_command", ToolSuccess: false, ToolError: "exit status 1",
			ToolDuration: time.Second},
	}
}

func otelExportDoc(t *testing.T, sessionID, vendor string, events []MetricEvent) map[string]any {
	t.Helper()
	data, err := ExportTraceOTLP(sessionID, vendor, "ep", "claude-x", time.Now(), events)
	if err != nil {
		t.Fatalf("ExportTraceOTLP returned error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ExportTraceOTLP returned empty data")
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("ExportTraceOTLP produced invalid JSON: %v", err)
	}
	return doc
}

func otelSpans(t *testing.T, doc map[string]any) []any {
	t.Helper()
	rs, ok := doc["resourceSpans"].([]any)
	if !ok || len(rs) != 1 {
		t.Fatalf("want exactly 1 resourceSpans entry, got %v", doc["resourceSpans"])
	}
	res := rs[0].(map[string]any)
	ra := res["resource"].(map[string]any)["attributes"].([]any)
	foundService := false
	for _, a := range ra {
		m := a.(map[string]any)
		if m["key"] == "service.name" {
			foundService = true
		}
	}
	if !foundService {
		t.Fatal("resource attributes missing service.name")
	}
	scopeSpans := res["scopeSpans"].([]any)
	return scopeSpans[0].(map[string]any)["spans"].([]any)
}

func findOTelSpan(t *testing.T, spans []any, prefix string) map[string]any {
	t.Helper()
	for _, s := range spans {
		m := s.(map[string]any)
		if name, _ := m["name"].(string); strings.HasPrefix(name, prefix) {
			return m
		}
	}
	t.Fatalf("span with name prefix %q not found", prefix)
	return nil
}

func otelSpanAttr(t *testing.T, span map[string]any, key string) map[string]any {
	t.Helper()
	attrs := span["attributes"].([]any)
	for _, a := range attrs {
		m := a.(map[string]any)
		if m["key"] == key {
			return m["value"].(map[string]any)
		}
	}
	t.Fatalf("attribute %q not found on span %v", key, span["name"])
	return nil
}

var (
	otelTraceIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	otelSpanIDRe  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func TestExportTraceOTLP_Structure(t *testing.T) {
	spans := otelSpans(t, otelExportDoc(t, "sess-otel", "anthropic", otelTestEvents()))
	// root session + 2 turns + 3 leaf spans (llm + 2 tools)
	if len(spans) != 6 {
		t.Fatalf("want 6 spans, got %d", len(spans))
	}
	for _, s := range spans {
		m := s.(map[string]any)
		if id, _ := m["spanId"].(string); !otelSpanIDRe.MatchString(id) {
			t.Fatalf("spanId %q is not 16 hex chars", id)
		}
		if id, _ := m["traceId"].(string); !otelTraceIDRe.MatchString(id) {
			t.Fatalf("traceId %q is not 32 hex chars", id)
		}
		if _, ok := m["startTimeUnixNano"].(string); !ok {
			t.Fatalf("startTimeUnixNano missing/non-string on span %v", m["name"])
		}
	}
	// The llm span's parent must be a "turn" span.
	llm := findOTelSpan(t, spans, "chat claude-x")
	parentID := llm["parentSpanId"].(string)
	var parentFound bool
	for _, s := range spans {
		if s.(map[string]any)["spanId"] == parentID {
			parentFound = true
		}
	}
	if !parentFound {
		t.Fatalf("llm span parentSpanId %q not found among spans", parentID)
	}
	// Session root has no parentSpanId.
	root := findOTelSpan(t, spans, "agent.run")
	if _, has := root["parentSpanId"]; has {
		t.Fatal("session root span should not have parentSpanId")
	}
}

func TestExportTraceOTLP_GenAIAttributes(t *testing.T) {
	spans := otelSpans(t, otelExportDoc(t, "sess-otel", "anthropic", otelTestEvents()))

	llm := findOTelSpan(t, spans, "chat claude-x")
	if llm["kind"] != "SPAN_KIND_CLIENT" {
		t.Fatalf("llm span kind = %v, want SPAN_KIND_CLIENT", llm["kind"])
	}
	if v := otelSpanAttr(t, llm, "gen_ai.operation.name")["stringValue"]; v != "chat" {
		t.Fatalf("gen_ai.operation.name = %v, want chat", v)
	}
	if v := otelSpanAttr(t, llm, "gen_ai.system")["stringValue"]; v != "anthropic" {
		t.Fatalf("gen_ai.system = %v, want anthropic", v)
	}
	if v := otelSpanAttr(t, llm, "gen_ai.usage.input_tokens")["intValue"]; v != "100" {
		t.Fatalf("gen_ai.usage.input_tokens = %v, want \"100\" (protojson int64-as-string)", v)
	}
	if v := otelSpanAttr(t, llm, "gen_ai.usage.output_tokens")["intValue"]; v != "50" {
		t.Fatalf("gen_ai.usage.output_tokens = %v, want \"50\"", v)
	}
	if v := otelSpanAttr(t, llm, "ggcode.cache_read_tokens")["intValue"]; v != "20" {
		t.Fatalf("ggcode.cache_read_tokens = %v, want \"20\"", v)
	}

	tool := findOTelSpan(t, spans, "execute_tool run_command")
	if v := otelSpanAttr(t, tool, "gen_ai.tool.name")["stringValue"]; v != "run_command" {
		t.Fatalf("gen_ai.tool.name = %v, want run_command", v)
	}
	if v := otelSpanAttr(t, tool, "error.type")["stringValue"]; v != "tool_failure" {
		t.Fatalf("error.type = %v, want tool_failure", v)
	}
	status, ok := tool["status"].(map[string]any)
	if !ok || status["code"] != "STATUS_CODE_ERROR" {
		t.Fatalf("failed tool span status = %v, want STATUS_CODE_ERROR", tool["status"])
	}
	if msg, _ := status["message"].(string); msg != "exit status 1" {
		t.Fatalf("status message = %v, want exit status 1", status["message"])
	}

	// Successful tool span must not carry an error status.
	okTool := findOTelSpan(t, spans, "execute_tool read_file")
	if _, has := okTool["status"]; has {
		t.Fatal("successful tool span should not have a status field")
	}
}

func TestExportTraceOTLP_Deterministic(t *testing.T) {
	events := otelTestEvents()
	a, err := ExportTraceOTLP("sess-otel", "anthropic", "ep", "claude-x", time.Now(), events)
	if err != nil {
		t.Fatalf("first export error: %v", err)
	}
	b, err := ExportTraceOTLP("sess-otel", "anthropic", "ep", "claude-x", time.Now(), events)
	if err != nil {
		t.Fatalf("second export error: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("OTLP export should be deterministic for the same events")
	}
}

func TestExportTraceOTLP_EmptyEvents(t *testing.T) {
	data, err := ExportTraceOTLP("empty-otel", "", "", "", time.Time{}, nil)
	if err != nil {
		t.Fatalf("ExportTraceOTLP error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	spans := otelSpans(t, doc)
	if len(spans) != 1 {
		t.Fatalf("want 1 span for empty events, got %d", len(spans))
	}
	root := spans[0].(map[string]any)
	if root["startTimeUnixNano"] != "0" {
		t.Fatalf("zero-time span startTimeUnixNano = %v, want \"0\"", root["startTimeUnixNano"])
	}
}

func TestGenAISystemMapping(t *testing.T) {
	cases := map[string]string{
		"anthropic": "anthropic",
		"OpenAI":    "openai",
		"google":    "gemini",
		"ZAI":       "zai",
		"":          "",
	}
	for in, want := range cases {
		if got := genAISystem(in); got != want {
			t.Fatalf("genAISystem(%q) = %q, want %q", in, got, want)
		}
	}
}
