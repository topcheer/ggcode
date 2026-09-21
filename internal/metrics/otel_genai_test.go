package metrics

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestExportOTLPTraceSemconv(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []MetricEvent{
		{
			Timestamp: base.Add(2 * time.Second), TurnIndex: 0, Type: "llm",
			Duration: 2 * time.Second, TTFT: 500 * time.Millisecond,
			InputTokens: 100, OutputTokens: 50, CacheRead: 10, CacheWrite: 5,
			Model: "gpt-test", Vendor: "GitHub Copilot",
		},
		{
			Timestamp: base.Add(3 * time.Second), TurnIndex: 0, Type: "tool",
			ToolName: "run_command", ToolSuccess: false, ToolError: "boom",
			ToolDuration: time.Second,
		},
	}
	data, err := ExportOTLPTrace("sess-1", "GitHub Copilot", "https://api.example.com", "gpt-test", base, events)
	if err != nil {
		t.Fatalf("ExportOTLPTrace: %v", err)
	}

	var req otlpExportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal OTLP/JSON: %v", err)
	}
	if len(req.ResourceSpans) != 1 {
		t.Fatalf("want 1 resourceSpans, got %d", len(req.ResourceSpans))
	}
	rs := req.ResourceSpans[0]
	if n := findOTLPAttr(rs.Resource.Attributes, "service.name"); n == nil || n.Value.StringValue == nil || *n.Value.StringValue != "ggcode" {
		t.Errorf("resource missing service.name=ggcode: %+v", rs.Resource.Attributes)
	}
	if len(rs.ScopeSpans) != 1 || len(rs.ScopeSpans[0].Spans) != 4 {
		t.Fatalf("want 4 spans (root+turn+llm+tool), got %d", len(rs.ScopeSpans[0].Spans))
	}
	spans := rs.ScopeSpans[0].Spans

	// Root span.
	root := spans[0]
	if root.Name != "invoke_agent ggcode" || root.ParentSpanID != "" || root.Kind != otlpKindInternal {
		t.Errorf("bad root span: %+v", root)
	}
	if len(root.TraceID) != 32 {
		t.Errorf("traceId must be 32 hex chars, got %q", root.TraceID)
	}
	if _, err := hex.DecodeString(root.TraceID); err != nil {
		t.Errorf("traceId not hex: %v", err)
	}
	if len(root.SpanID) != 16 {
		t.Errorf("spanId must be 16 hex chars, got %q", root.SpanID)
	}
	if a := findOTLPAttr(root.Attributes, "gen_ai.operation.name"); a == nil || *a.Value.StringValue != "invoke_agent" {
		t.Errorf("root missing gen_ai.operation.name=invoke_agent")
	}
	if a := findOTLPAttr(root.Attributes, "gen_ai.conversation.id"); a == nil || *a.Value.StringValue != "sess-1" {
		t.Errorf("root missing gen_ai.conversation.id")
	}
	if a := findOTLPAttr(root.Attributes, "gen_ai.provider.name"); a == nil || *a.Value.StringValue != "github.copilot" {
		t.Errorf("root gen_ai.provider.name = %+v, want github.copilot", a)
	}
	if root.StartTimeUnixNano != formatNano(base) {
		t.Errorf("root start = %q, want %q", root.StartTimeUnixNano, formatNano(base))
	}

	// Turn span wraps children.
	turn := spans[1]
	if turn.Name != "turn 0" || turn.ParentSpanID != root.SpanID {
		t.Errorf("bad turn span: %+v", turn)
	}

	// LLM span: CLIENT kind + usage + TTFT per semconv.
	llm := spans[2]
	if llm.Kind != otlpKindClient {
		t.Errorf("llm span kind = %d, want %d (CLIENT)", llm.Kind, otlpKindClient)
	}
	if llm.Name != "chat gpt-test" {
		t.Errorf("llm span name = %q", llm.Name)
	}
	if a := findOTLPAttr(llm.Attributes, "gen_ai.usage.input_tokens"); a == nil || a.Value.IntValue == nil || *a.Value.IntValue != "100" {
		t.Errorf("llm gen_ai.usage.input_tokens = %+v", a)
	}
	if a := findOTLPAttr(llm.Attributes, "gen_ai.usage.output_tokens"); a == nil || a.Value.IntValue == nil || *a.Value.IntValue != "50" {
		t.Errorf("llm gen_ai.usage.output_tokens = %+v", a)
	}
	if a := findOTLPAttr(llm.Attributes, "gen_ai.usage.cache_read.input_tokens"); a == nil || a.Value.IntValue == nil || *a.Value.IntValue != "10" {
		t.Errorf("llm cache_read tokens = %+v", a)
	}
	if a := findOTLPAttr(llm.Attributes, "gen_ai.response.time_to_first_chunk"); a == nil || a.Value.DoubleValue == nil || *a.Value.DoubleValue != 0.5 {
		t.Errorf("llm TTFT attr = %+v", a)
	}
	if llm.ParentSpanID != turn.SpanID {
		t.Errorf("llm parent = %q, want turn span %q", llm.ParentSpanID, turn.SpanID)
	}

	// Tool span: error status + gen_ai.tool.name.
	tool := spans[3]
	if tool.Name != "execute_tool run_command" {
		t.Errorf("tool span name = %q", tool.Name)
	}
	if a := findOTLPAttr(tool.Attributes, "gen_ai.tool.name"); a == nil || *a.Value.StringValue != "run_command" {
		t.Errorf("tool gen_ai.tool.name = %+v", a)
	}
	if tool.Status == nil || tool.Status.Code != otlpStatusError || tool.Status.Message != "boom" {
		t.Errorf("tool span status = %+v, want ERROR/boom", tool.Status)
	}
	if a := findOTLPAttr(tool.Attributes, "error.type"); a == nil || *a.Value.StringValue != "tool_execution_error" {
		t.Errorf("tool error.type = %+v", a)
	}
}

func TestExportOTLPTraceEmpty(t *testing.T) {
	data, err := ExportOTLPTrace("sess-empty", "zai", "", "", time.Time{}, nil)
	if err != nil {
		t.Fatalf("ExportOTLPTrace: %v", err)
	}
	var req otlpExportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spans := req.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 1 || spans[0].Name != "invoke_agent ggcode" {
		t.Fatalf("want root-only trace, got %+v", spans)
	}
}

func TestNormalizeProviderName(t *testing.T) {
	cases := map[string]string{
		"GitHub Copilot": "github.copilot",
		"ZAI":            "zai",
		"Anthropic":      "anthropic",
		"  OpenAI ":      "openai",
		"some_vendor":    "some.vendor",
	}
	for in, want := range cases {
		if got := normalizeProviderName(in); got != want {
			t.Errorf("normalizeProviderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func findOTLPAttr(attrs []otlpAttr, key string) *otlpAttr {
	for i := range attrs {
		if attrs[i].Key == key {
			return &attrs[i]
		}
	}
	return nil
}

func formatNano(ts time.Time) string {
	return strconv.FormatInt(ts.UnixNano(), 10)
}
