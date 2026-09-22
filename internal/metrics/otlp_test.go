package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestResolveOTLPEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	if got := ResolveOTLPEndpoint(""); got != "" {
		t.Fatalf("empty config and env: want \"\", got %q", got)
	}
	if got := ResolveOTLPEndpoint("http://localhost:4318"); got != "http://localhost:4318/v1/traces" {
		t.Fatalf("base url: want /v1/traces appended, got %q", got)
	}
	if got := ResolveOTLPEndpoint("http://localhost:4318/v1/traces"); got != "http://localhost:4318/v1/traces" {
		t.Fatalf("full url should be kept as-is, got %q", got)
	}
	if got := ResolveOTLPEndpoint("http://localhost:4318/"); got != "http://localhost:4318/v1/traces" {
		t.Fatalf("trailing slash: got %q", got)
	}
	// Generic env var is treated as a base URL per the OTel env spec.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	if got := ResolveOTLPEndpoint(""); got != "http://collector:4318/v1/traces" {
		t.Fatalf("generic env base url: got %q", got)
	}
	// Signal-specific env var is used as-is and wins over the generic one.
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://traces:4318/custom/path")
	if got := ResolveOTLPEndpoint(""); got != "http://traces:4318/custom/path" {
		t.Fatalf("signal-specific env: got %q", got)
	}
	// Explicit config wins over env.
	if got := ResolveOTLPEndpoint("http://cfg:4318"); got != "http://cfg:4318/v1/traces" {
		t.Fatalf("config should win over env: got %q", got)
	}
}

// collectOTLPRequest is a test backend that captures one export request.
type collectOTLPRequest struct {
	mu      sync.Mutex
	bodies  []map[string]any
	server  *httptest.Server
	got     chan struct{}
	status  int
	fails   int
	onReqCb func(r *http.Request)
}

func newCollectOTLPRequest(t *testing.T, status int) *collectOTLPRequest {
	c := &collectOTLPRequest{got: make(chan struct{}, 16), status: status}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		c.mu.Lock()
		c.bodies = append(c.bodies, body)
		c.mu.Unlock()
		w.WriteHeader(c.status)
		c.got <- struct{}{}
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *collectOTLPRequest) spans(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("no export requests received")
	}
	rs, ok := c.bodies[0]["resourceSpans"].([]any)
	if !ok || len(rs) != 1 {
		t.Fatal("want exactly one resourceSpans entry")
	}
	entry := rs[0].(map[string]any)
	scopeSpans, ok := entry["scopeSpans"].([]any)
	if !ok || len(scopeSpans) != 1 {
		t.Fatal("want exactly one scopeSpans entry")
	}
	spans, ok := scopeSpans[0].(map[string]any)["spans"].([]any)
	if !ok || len(spans) == 0 {
		t.Fatal("want at least one span")
	}
	res := entry["resource"].(map[string]any)
	attrs := res["attributes"].([]any)
	foundService := false
	for _, a := range attrs {
		am := a.(map[string]any)
		if am["key"] == "service.name" {
			foundService = true
		}
	}
	if !foundService {
		t.Fatal("resource attributes missing service.name")
	}
	out := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.(map[string]any))
	}
	return out
}

func attrMap(span map[string]any) map[string]any {
	out := map[string]any{}
	for _, a := range span["attributes"].([]any) {
		am := a.(map[string]any)
		out[am["key"].(string)] = am["value"].(map[string]any)
	}
	return out
}

func TestOTLPExporterExportSpans(t *testing.T) {
	c := newCollectOTLPRequest(t, http.StatusOK)
	spanIDs := 0
	e := NewOTLPExporter(OTLPConfig{
		Endpoint:  c.server.URL,
		QueueSize: 16,
		Timeout:   2 * time.Second,
		NewSpanID: func() string { spanIDs++; return fmt.Sprintf("%016x", spanIDs) },
		Now:       func() time.Time { return time.Unix(1700000000, 0) },
		Headers:   map[string]string{"X-Test": "1"},
	})
	if e == nil {
		t.Fatal("exporter must not be nil with endpoint set")
	}
	end := time.Unix(1700000100, 0)
	llm := MetricEvent{
		Timestamp: end, TurnIndex: 3, Type: "llm",
		Duration: 2 * time.Second, TTFT: 300 * time.Millisecond,
		InputTokens: 100, OutputTokens: 50, CacheRead: 10, CacheWrite: 5,
		Model: "gpt-x", Vendor: "OpenAI",
	}
	toolFail := MetricEvent{
		Timestamp: end, TurnIndex: 3, Type: "tool",
		ToolName: "run_command", ToolSuccess: false, ToolError: "boom", ToolDuration: 10 * time.Millisecond,
	}
	toolOK := MetricEvent{
		Timestamp: end, TurnIndex: 4, Type: "tool",
		ToolName: "read_file", ToolSuccess: true, ToolDuration: 5 * time.Millisecond,
	}
	e.Emit(llm)
	e.Emit(toolFail)
	e.Emit(toolOK)
	e.Flush()

	spans := c.spans(t)
	if len(spans) != 3 {
		t.Fatalf("want 3 spans, got %d", len(spans))
	}
	traceIDs := map[string]bool{}
	for _, s := range spans {
		id := s["traceId"].(string)
		traceIDs[id] = true
		if len(id) != 32 {
			t.Errorf("traceId must be 32 hex chars, got %q", id)
		}
		if sid := s["spanId"].(string); len(sid) != 16 {
			t.Errorf("spanId must be 16 hex chars, got %q", sid)
		}
		startNs := parseNano(t, s["startTimeUnixNano"].(string))
		endNs := parseNano(t, s["endTimeUnixNano"].(string))
		if endNs != end.UnixNano() {
			t.Errorf("endTimeUnixNano = %d, want %d", endNs, end.UnixNano())
		}
		if startNs >= endNs {
			t.Errorf("span start %d must be before end %d", startNs, endNs)
		}
	}
	if len(traceIDs) != 1 {
		t.Fatalf("all spans must share one traceId, got %d", len(traceIDs))
	}

	// LLM span per GenAI semconv.
	byName := map[string]map[string]any{}
	for _, s := range spans {
		byName[s["name"].(string)] = s
	}
	llmSpan, ok := byName["chat gpt-x"]
	if !ok {
		t.Fatalf("missing LLM span %q, have %v", "chat gpt-x", spanNames(spans))
	}
	if llmSpan["kind"].(float64) != float64(spanKindClient) {
		t.Errorf("LLM span kind should be CLIENT(%d)", spanKindClient)
	}
	am := attrMap(llmSpan)
	for k, want := range map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.system":         "openai",
		"gen_ai.request.model":  "gpt-x",
	} {
		if got := am[k].(map[string]any)["stringValue"]; got != want {
			t.Errorf("attr %s = %v, want %q", k, got, want)
		}
	}
	// Proto3 JSON: int64 attributes are strings.
	for k, want := range map[string]string{
		"gen_ai.usage.input_tokens":                "100",
		"gen_ai.usage.output_tokens":               "50",
		"gen_ai.usage.cache_read.input_tokens":     "10",
		"gen_ai.usage.cache_creation.input_tokens": "5",
		"ggcode.ttft_ms":                           "300",
		"ggcode.turn_index":                        "3",
	} {
		if got := am[k].(map[string]any)["intValue"]; got != want {
			t.Errorf("attr %s = %v, want %q", k, got, want)
		}
	}

	// Failed tool span carries an ERROR status.
	failSpan, ok := byName["execute_tool run_command"]
	if !ok {
		t.Fatalf("missing failed tool span, have %v", spanNames(spans))
	}
	st := failSpan["status"].(map[string]any)
	if st["code"].(float64) != float64(statusCodeFail) || st["message"] != "boom" {
		t.Errorf("failed tool status = %v, want code %d message boom", st, statusCodeFail)
	}
	// Successful tool span has no status.
	okSpan := byName["execute_tool read_file"]
	if _, has := okSpan["status"]; has {
		t.Errorf("successful tool span must not carry a status")
	}

	exported, dropped, failed := e.Stats()
	if exported != 3 || dropped != 0 || failed != 0 {
		t.Errorf("stats = (%d,%d,%d), want (3,0,0)", exported, dropped, failed)
	}
	e.Stop()
}

func parseNano(t *testing.T, s string) int64 {
	t.Helper()
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		t.Fatalf("parse nano %q: %v", s, err)
	}
	return v
}

func spanNames(spans []map[string]any) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s["name"].(string))
	}
	return out
}

func TestOTLPExporterStopFlushesPending(t *testing.T) {
	c := newCollectOTLPRequest(t, http.StatusOK)
	e := NewOTLPExporter(OTLPConfig{
		Endpoint:  c.server.URL,
		QueueSize: 8,
		NewSpanID: func() string { return "aaaaaaaaaaaaaaaa" },
	})
	e.Emit(MetricEvent{Timestamp: time.Now(), Type: "tool", ToolName: "grep", ToolSuccess: true})
	e.Stop() // must drain and send
	select {
	case <-c.got:
	default:
		t.Fatal("Stop did not flush pending spans")
	}
	e.Stop() // idempotent
}

func TestOTLPExporterDropOnFullDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // block the single in-flight export
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer once.Do(func() { close(release) })
	e := NewOTLPExporter(OTLPConfig{
		Endpoint:  srv.URL + "/v1/traces",
		QueueSize: 1,
		Timeout:   5 * time.Second,
		NewSpanID: func() string { return "bbbbbbbbbbbbbbbb" },
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			e.Emit(MetricEvent{Timestamp: time.Now(), Type: "tool", ToolName: "x", ToolSuccess: true})
		}
	}()
	select {
	case <-done:
		// Emit must never block even with a stalled backend.
	case <-time.After(3 * time.Second):
		t.Fatal("Emit blocked on a full queue")
	}
	once.Do(func() { close(release) })
	e.Stop()
	if _, dropped, _ := e.Stats(); dropped == 0 {
		t.Skip("queue drained faster than expected; drop path untested")
	}
}

func TestOTLPExporterErrorCallback(t *testing.T) {
	var errs int
	var mu sync.Mutex
	errFn := func(err error) {
		mu.Lock()
		errs++
		mu.Unlock()
	}
	c := newCollectOTLPRequest(t, http.StatusInternalServerError)
	e := NewOTLPExporter(OTLPConfig{
		Endpoint:  c.server.URL,
		QueueSize: 8,
		Timeout:   2 * time.Second,
		NewSpanID: func() string { return "cccccccccccccccc" },
		OnError:   errFn,
	})
	e.Emit(MetricEvent{Timestamp: time.Now(), Type: "tool", ToolName: "x", ToolSuccess: true})
	e.Flush()
	mu.Lock()
	defer mu.Unlock()
	if errs != 1 {
		t.Fatalf("OnError called %d times, want 1", errs)
	}
	if _, _, failed := e.Stats(); failed != 1 {
		t.Fatalf("failed counter = %d, want 1", failed)
	}
	e.Stop()
}

func TestNewOTLPExporterNilWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	if e := NewOTLPExporter(OTLPConfig{Endpoint: " "}); e != nil {
		t.Fatal("blank endpoint must yield nil exporter")
	}
	// Deriving the trace ID from a session ID must be deterministic.
	a := deriveTraceID("sess-123")
	b := deriveTraceID("sess-123")
	if a != b || len(a) != 32 {
		t.Fatalf("deterministic trace id broken: %q vs %q", a, b)
	}
	c := deriveTraceID("sess-other")
	if c == a {
		t.Fatalf("different sessions must derive different trace ids: %q", a)
	}
}
