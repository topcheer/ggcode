package metrics

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// This file implements a lightweight, dependency-free OTLP/HTTP (JSON
// protobuf-JSON encoding) trace exporter following the OpenTelemetry GenAI
// semantic conventions. Every LLM API call and tool execution becomes one
// span, batched and pushed live to any OTLP-compatible observability backend
// (Jaeger, Grafana Tempo, Langfuse, Datadog, Aspire, ...).
//
// Design constraints mirror metrics.Collector: fire-and-forget emission that
// never blocks or slows the agent loop (events are dropped when the queue is
// full), one background goroutine, and no external dependencies.

const (
	// Default OTLP/HTTP JSON ingest path appended to base endpoints.
	otlpTracesPath = "/v1/traces"

	defaultOTLPFlushInterval = 5 * time.Second
	minOTLPFlushInterval     = 1 * time.Second
	defaultOTLPQueueSize     = 256
	defaultOTLPMaxBatch      = 64
	defaultOTLPTimeout       = 5 * time.Second
	defaultOTLPServiceName   = "ggcode"

	// OTLP span kinds (proto enum values).
	spanKindInternal = 1
	spanKindClient   = 3
	// OTLP span status codes.
	statusCodeOK   = 1
	statusCodeFail = 2
)

// OTLPConfig configures the live OTLP trace exporter. Endpoint must be
// resolved (non-empty); use ResolveOTLPEndpoint to apply env fallbacks.
type OTLPConfig struct {
	// Endpoint is the full OTLP/HTTP traces ingest URL.
	Endpoint string
	// Headers are extra HTTP headers sent with every export request
	// (e.g. Authorization for hosted backends).
	Headers map[string]string
	// FlushInterval is how often pending spans are batched and pushed.
	// Defaults to 5s, floored at 1s.
	FlushInterval time.Duration
	// QueueSize is the pending-event channel buffer. Defaults to 256.
	QueueSize int
	// MaxBatchSize caps spans per HTTP request. Defaults to 64.
	MaxBatchSize int
	// Timeout bounds each export HTTP request. Defaults to 5s.
	Timeout time.Duration
	// ServiceName is reported as the service.name resource attribute.
	// Defaults to "ggcode".
	ServiceName string
	// SessionID optionally pins the trace ID deterministically from the
	// session identifier; otherwise a random trace ID is generated once per
	// exporter (one trace per ggcode process).
	SessionID string
	// DefaultModel / DefaultVendor backfill spans for events that carry no
	// model/vendor metadata (some emitters fill these in later layers).
	DefaultModel  string
	DefaultVendor string

	// HTTPClient overrides the default HTTP client (tests).
	HTTPClient *http.Client
	// Now overrides wall clock (tests).
	Now func() time.Time
	// NewSpanID returns a fresh 8-byte span ID per call (tests).
	NewSpanID func() string
	// OnError is invoked for export failures in addition to debug logging.
	OnError func(err error)
}

// OTLPExporter pushes MetricEvents as GenAI-semconv OTLP spans over
// OTLP/HTTP JSON. Emission is non-blocking; a full queue drops events.
type OTLPExporter struct {
	cfg     OTLPConfig
	ch      chan MetricEvent
	flushCh chan chan struct{}
	stopCh  chan struct{}
	doneCh  chan struct{}

	stopOnce sync.Once

	traceID  string
	newSpanF func() string
	client   *http.Client

	exported atomic.Uint64
	dropped  atomic.Uint64
	failed   atomic.Uint64
}

// NewOTLPExporter starts the background batching/export goroutine. Returns
// nil when endpoint is empty (caller should skip wiring entirely).
func NewOTLPExporter(cfg OTLPConfig) *OTLPExporter {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultOTLPFlushInterval
	}
	if cfg.FlushInterval < minOTLPFlushInterval {
		cfg.FlushInterval = minOTLPFlushInterval
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultOTLPQueueSize
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = defaultOTLPMaxBatch
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultOTLPTimeout
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = defaultOTLPServiceName
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewSpanID != nil {
		// keep test hook
	} else {
		cfg.NewSpanID = func() string {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			return hex.EncodeToString(b)
		}
	}
	e := &OTLPExporter{
		cfg:      cfg,
		ch:       make(chan MetricEvent, cfg.QueueSize),
		flushCh:  make(chan chan struct{}),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		traceID:  deriveTraceID(cfg.SessionID),
		newSpanF: cfg.NewSpanID,
		client:   cfg.HTTPClient,
	}
	if e.client == nil {
		e.client = &http.Client{Timeout: cfg.Timeout}
	}
	safego.Go("metrics.otlp", e.run)
	return e
}

// Emit enqueues a metric event for OTLP export. Non-blocking: when the queue
// is full the event is dropped and the dropped counter is incremented.
func (e *OTLPExporter) Emit(ev MetricEvent) {
	if e == nil {
		return
	}
	select {
	case e.ch <- ev:
	default:
		e.dropped.Add(1)
	}
}

// Flush pushes all pending spans and waits until the request completes.
// Safe to call after Stop (no-op).
func (e *OTLPExporter) Flush() {
	if e == nil {
		return
	}
	select {
	case <-e.doneCh:
		return
	default:
	}
	ack := make(chan struct{})
	select {
	case e.flushCh <- ack:
		<-ack
	case <-e.doneCh:
	}
}

// Stop flushes pending spans and terminates the export loop. Idempotent.
func (e *OTLPExporter) Stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() { close(e.stopCh) })
	<-e.doneCh
}

// Stats returns cumulative exported / dropped / failed span counts.
func (e *OTLPExporter) Stats() (exported, dropped, failed uint64) {
	if e == nil {
		return 0, 0, 0
	}
	return e.exported.Load(), e.dropped.Load(), e.failed.Load()
}

// run is the batching loop. Batches are flushed when full, on the ticker, on
// explicit Flush, and on Stop (drain).
func (e *OTLPExporter) run() {
	defer close(e.doneCh)
	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()
	pending := make([]otlpSpan, 0, e.cfg.MaxBatchSize)
	send := func() {
		if len(pending) == 0 {
			return
		}
		e.export(pending)
		pending = pending[:0]
	}
	for {
		select {
		case ev := <-e.ch:
			pending = append(pending, otlpSpanFromEvent(ev, e.traceID, e.newSpanF, e.cfg))
			if len(pending) >= e.cfg.MaxBatchSize {
				send()
			}
		case <-ticker.C:
			send()
		case ack := <-e.flushCh:
			for {
				select {
				case ev := <-e.ch:
					pending = append(pending, otlpSpanFromEvent(ev, e.traceID, e.newSpanF, e.cfg))
				default:
					send()
					close(ack)
					goto next
				}
			}
		case <-e.stopCh:
			for {
				select {
				case ev := <-e.ch:
					pending = append(pending, otlpSpanFromEvent(ev, e.traceID, e.newSpanF, e.cfg))
				default:
					send()
					return
				}
			}
		}
	next:
	}
}

// export posts one batch as an OTLP/HTTP JSON ExportTraceServiceRequest.
func (e *OTLPExporter) export(spans []otlpSpan) {
	for len(spans) > e.cfg.MaxBatchSize { // defensive chunking
		e.export(spans[:e.cfg.MaxBatchSize])
		spans = spans[e.cfg.MaxBatchSize:]
	}
	if len(spans) == 0 {
		return
	}
	req := otlpExportRequest{
		ResourceSpans: []otlpResourceSpans{{
			Resource: &otlpResource{
				Attributes: []otlpAttr{
					strAttr("service.name", e.cfg.ServiceName),
					strAttr("telemetry.sdk.name", "ggcode"),
				},
			},
			ScopeSpans: []otlpScopeSpans{{
				Scope: &otlpScope{Name: "github.com/topcheer/ggcode/internal/metrics"},
				Spans: spans,
			}},
		}},
	}
	body, err := json.Marshal(req)
	if err != nil {
		e.fail(fmt.Errorf("otlp marshal: %w", err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		e.fail(fmt.Errorf("otlp request: %w", err))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range e.cfg.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		e.fail(fmt.Errorf("otlp post %s: %w", e.cfg.Endpoint, err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e.fail(fmt.Errorf("otlp endpoint %s returned status %d", e.cfg.Endpoint, resp.StatusCode))
		return
	}
	e.exported.Add(uint64(len(spans)))
}

func (e *OTLPExporter) fail(err error) {
	e.failed.Add(1)
	debug.Log("metrics", "OTLP export: %v", err)
	if e.cfg.OnError != nil {
		e.cfg.OnError(err)
	}
}

// ResolveOTLPEndpoint resolves the effective OTLP traces endpoint:
// explicit config wins, then the signal-specific env var (used as-is, per
// the OTel env spec), then the generic env var (treated as a base URL).
// Base URLs get /v1/traces appended. Returns "" when nothing is configured.
func ResolveOTLPEndpoint(configured string) string {
	if v := strings.TrimSpace(configured); v != "" {
		return joinOTLPTracesPath(v)
	}
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); v != "" {
		return joinOTLPTracesPath(v)
	}
	return ""
}

func joinOTLPTracesPath(endpoint string) string {
	ep := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(ep, otlpTracesPath) {
		return ep
	}
	return ep + otlpTracesPath
}

// deriveTraceID returns a 16-byte hex trace ID, deterministic from the
// session ID when available, otherwise random per exporter instance.
func deriveTraceID(sessionID string) string {
	if strings.TrimSpace(sessionID) != "" {
		sum := sha256.Sum256([]byte(sessionID))
		return hex.EncodeToString(sum[:16])
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- OTLP protobuf-JSON wire types (OTLP spec v1, trace service) ---

type otlpExportRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   *otlpResource    `json:"resource,omitempty"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpAttr `json:"attributes,omitempty"`
}

type otlpScopeSpans struct {
	Scope *otlpScope `json:"scope,omitempty"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	Name              string      `json:"name"`
	Kind              int         `json:"kind"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	EndTimeUnixNano   string      `json:"endTimeUnixNano"`
	Attributes        []otlpAttr  `json:"attributes,omitempty"`
	Status            *otlpStatus `json:"status,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpAttr struct {
	Key   string        `json:"key"`
	Value otlpAttrValue `json:"value"`
}

// otlpAttrValue mirrors the OTLP AnyValue oneof. Proto3 JSON encodes int64
// as a string, so intValue is a string on the wire.
type otlpAttrValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

func strAttr(key, val string) otlpAttr {
	return otlpAttr{Key: key, Value: otlpAttrValue{StringValue: &val}}
}

func intAttr(key string, val int64) otlpAttr {
	s := fmt.Sprintf("%d", val)
	return otlpAttr{Key: key, Value: otlpAttrValue{IntValue: &s}}
}

// otlpSpanFromEvent converts a completed metric event into one OTLP span per
// the GenAI semantic conventions:
//   - LLM call  -> span "chat <model>" (kind CLIENT), gen_ai.usage.* counters
//   - tool call -> span "execute_tool <name>" (kind INTERNAL), OTLP ERROR
//     status when the tool failed
//
// The event timestamp is the completion time; the span starts at
// completion minus the recorded duration.
func otlpSpanFromEvent(ev MetricEvent, traceID string, newSpanID func() string, cfg OTLPConfig) otlpSpan {
	now := time.Now()
	if cfg.Now != nil {
		now = cfg.Now()
	}
	end := ev.Timestamp
	if end.IsZero() {
		end = now
	}
	dur := ev.Duration
	if ev.Type == "tool" {
		dur = ev.ToolDuration
	}
	if dur < 0 {
		dur = 0
	}
	start := end.Add(-dur)

	span := otlpSpan{
		TraceID:           traceID,
		SpanID:            newSpanID(),
		StartTimeUnixNano: fmt.Sprintf("%d", start.UnixNano()),
		EndTimeUnixNano:   fmt.Sprintf("%d", end.UnixNano()),
		Attributes:        []otlpAttr{intAttr("ggcode.turn_index", int64(ev.TurnIndex))},
	}
	switch ev.Type {
	case "llm":
		model := ev.Model
		if model == "" {
			model = cfg.DefaultModel
		}
		vendor := ev.Vendor
		if vendor == "" {
			vendor = cfg.DefaultVendor
		}
		span.Name = strings.TrimSpace("chat " + model)
		span.Kind = spanKindClient
		span.Attributes = append(span.Attributes,
			strAttr("gen_ai.operation.name", "chat"),
		)
		if vendor != "" {
			span.Attributes = append(span.Attributes, strAttr("gen_ai.system", strings.ToLower(vendor)))
		}
		if model != "" {
			span.Attributes = append(span.Attributes, strAttr("gen_ai.request.model", model))
		}
		if ev.InputTokens > 0 {
			span.Attributes = append(span.Attributes, intAttr("gen_ai.usage.input_tokens", int64(ev.InputTokens)))
		}
		if ev.OutputTokens > 0 {
			span.Attributes = append(span.Attributes, intAttr("gen_ai.usage.output_tokens", int64(ev.OutputTokens)))
		}
		if ev.CacheRead > 0 {
			span.Attributes = append(span.Attributes, intAttr("gen_ai.usage.cache_read.input_tokens", int64(ev.CacheRead)))
		}
		if ev.CacheWrite > 0 {
			span.Attributes = append(span.Attributes, intAttr("gen_ai.usage.cache_creation.input_tokens", int64(ev.CacheWrite)))
		}
		if ev.TTFT > 0 {
			span.Attributes = append(span.Attributes, intAttr("ggcode.ttft_ms", ev.TTFT.Milliseconds()))
		}
		if ev.ThinkTime > 0 {
			span.Attributes = append(span.Attributes, intAttr("ggcode.think_time_ms", ev.ThinkTime.Milliseconds()))
		}
	default: // "tool" and anything unrecognized: treat as tool execution
		name := ev.ToolName
		if name == "" {
			name = "tool"
		}
		span.Name = "execute_tool " + name
		span.Kind = spanKindInternal
		span.Attributes = append(span.Attributes,
			strAttr("gen_ai.operation.name", "execute_tool"),
			strAttr("gen_ai.tool.name", name),
		)
		if !ev.ToolSuccess && ev.ToolError != "" {
			span.Status = &otlpStatus{Code: statusCodeFail, Message: ev.ToolError}
			span.Attributes = append(span.Attributes, strAttr("error.type", "tool_execution_error"))
		}
	}
	return span
}
