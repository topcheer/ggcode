package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #5699: MCP deferred answer — Esc-pending-answer queue for input_required
// tool calls. Full chain: Esc (defer sentinel) → queue → answer → resume with
// carried requestState, plus TTL expiry, queue depth bound, restart
// degradation, and re-queue on follow-up rounds.

// pending69Server is a fake MCP server over HTTP that runs the MRTR
// input_required protocol:
//
//	first tools/call           → input_required (request r1, state st-1)
//	retry carrying st-1        → configured per-test behavior
type pending69Server struct {
	mu sync.Mutex
	// retries records every tools/call request that carried inputResponses.
	retries []map[string]json.RawMessage
	// retryStates records requestState values received on retries.
	retryStates []string
	// script[i] is the response for the i-th retried call (index 0 = first
	// retry). Entries: "complete", "ask2" (second input_required round),
	// "task" (completed task envelope; result served via tasks/result).
	script []string
}

func (s *pending69Server) handle(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req mcpTestServerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			sseResult(w, req.ID, map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "p69-fake", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tasks/result":
			sseResult(w, req.ID, map[string]any{
				"content": []any{map[string]string{"type": "text", "text": "task-done-after-answer"}},
			})
		case "tools/call":
			var params struct {
				Name           string                     `json:"name"`
				Arguments      map[string]any             `json:"arguments,omitempty"`
				InputResponses map[string]json.RawMessage `json:"inputResponses,omitempty"`
				RequestState   string                     `json:"requestState,omitempty"`
				Task           json.RawMessage            `json:"task,omitempty"`
			}
			_ = json.Unmarshal(req.Params, &params)
			s.mu.Lock()
			if len(params.InputResponses) > 0 || params.RequestState != "" {
				s.retries = append(s.retries, params.InputResponses)
				s.retryStates = append(s.retryStates, params.RequestState)
				idx := len(s.retries) - 1
				var behavior = "complete"
				if idx < len(s.script) {
					behavior = s.script[idx]
				}
				switch behavior {
				case "ask2":
					s.mu.Unlock()
					sseResult(w, req.ID, map[string]any{
						"resultType": "input_required",
						"inputRequests": map[string]any{
							"r2": map[string]any{
								"method":  "elicitation/create",
								"message": "Follow-up: which region?",
								"requestedSchema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"region": map[string]any{"type": "string"},
									},
								},
							},
						},
						"requestState": "st-2",
					})
					return
				case "task":
					s.mu.Unlock()
					sseResult(w, req.ID, map[string]any{
						"resultType": "task",
						"taskId":     "t-69",
						"status":     "completed",
					})
					return
				default:
					s.mu.Unlock()
					sseResult(w, req.ID, map[string]any{
						"content": []any{map[string]string{"type": "text", "text": "done-after-answer"}},
					})
					return
				}
			}
			s.mu.Unlock()
			// First call: ask the question.
			sseResult(w, req.ID, map[string]any{
				"resultType": "input_required",
				"inputRequests": map[string]any{
					"r1": map[string]any{
						"method":  "elicitation/create",
						"message": "Which city?",
						"requestedSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"city": map[string]any{"type": "string"},
							},
						},
					},
				},
				"requestState": "st-1",
			})
		default:
			sseResult(w, req.ID, map[string]any{})
		}
	}
}

func pending69Client(t *testing.T, srv *httptest.Server, name string) *Client {
	t.Helper()
	c := NewClientFromConfig(config.MCPServerConfig{Name: name, Type: "http", URL: srv.URL})
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestIssue5699EscQueueAnswerResumeFullChain covers the acceptance criterion:
// Esc → queue → answer → resume (fake MCP server), asserting the retry
// carries both the user's inputResponses and the original requestState.
func TestIssue5699EscQueueAnswerResumeFullChain(t *testing.T) {
	srv := pending69Server{}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-full")

	// Esc is simulated at the handler layer: the elicitation handler returns
	// the defer sentinel exactly like newMCPElicitationHandler does on
	// AskUserStatusCancelled with a live context.
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})

	_, err := c.CallTool(context.Background(), "deploy", map[string]interface{}{"env": "prod"})
	if err == nil {
		t.Fatal("expected deferred error from CallTool")
	}
	var paErr *PendingAnswerError
	if !errors.As(err, &paErr) {
		t.Fatalf("want *PendingAnswerError, got: %v", err)
	}
	id := paErr.PA.ID
	if paErr.PA.Server != "p69-full" || paErr.PA.ToolName != "deploy" {
		t.Fatalf("record mis-scoped: %+v", paErr.PA)
	}
	if paErr.PA.RequestState != "st-1" || len(paErr.PA.InputRequests) != 1 {
		t.Fatalf("record missing round state: %+v", paErr.PA)
	}
	if paErr.PA.Status != TaskStatusInputRequired || paErr.PA.TaskID != id {
		t.Fatalf("record not task-envelope shaped: %+v", paErr.PA.Task)
	}
	if n := c.PendingAnswerCount(); n != 1 {
		t.Fatalf("queue depth = %d, want 1", n)
	}

	// Answer with freeform text mapped onto the single-field schema.
	responses, err := BuildPendingAnswerResponses(paErr.PA.InputRequests, "Paris", false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.AnswerPendingInput(context.Background(), id, responses)
	if err != nil {
		t.Fatalf("answer/resume failed: %v", err)
	}
	if res.Result == nil {
		t.Fatalf("want completed result, got %+v", res)
	}
	if got := SummarizeCallToolResult(res.Result, 0); got != "done-after-answer" {
		t.Fatalf("resumed result = %q", got)
	}

	// The retry must carry the response for r1 AND the carried requestState.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.retries) != 1 {
		t.Fatalf("server saw %d retries, want 1", len(srv.retries))
	}
	if _, ok := srv.retries[0]["r1"]; !ok {
		t.Fatalf("retry missing r1 response: %+v", srv.retries[0])
	}
	if len(srv.retryStates) != 1 || srv.retryStates[0] != "st-1" {
		t.Fatalf("retry requestState = %v, want [st-1]", srv.retryStates)
	}

	// Queue is drained after a successful answer.
	if n := c.PendingAnswerCount(); n != 0 {
		t.Fatalf("queue depth after answer = %d, want 0", n)
	}
}

// TestIssue5699RequeueOnFollowUpRound asserts answering never blocks: a
// follow-up input_required round is re-queued as a fresh pending answer.
func TestIssue5699RequeueOnFollowUpRound(t *testing.T) {
	srv := pending69Server{script: []string{"ask2", "complete"}}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-requeue")
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})

	_, err := c.CallTool(context.Background(), "deploy", nil)
	var paErr *PendingAnswerError
	if !errors.As(err, &paErr) {
		t.Fatalf("want defer, got: %v", err)
	}

	resp1, err := BuildPendingAnswerResponses(paErr.PA.InputRequests, "Paris", false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.AnswerPendingInput(context.Background(), paErr.PA.ID, resp1)
	if err != nil {
		t.Fatalf("first answer failed: %v", err)
	}
	if res.Requeued == nil {
		t.Fatalf("want re-queued follow-up round, got %+v", res)
	}
	if res.Requeued.RequestState != "st-2" {
		t.Fatalf("follow-up record state = %q, want st-2", res.Requeued.RequestState)
	}

	resp2, err := BuildPendingAnswerResponses(res.Requeued.InputRequests, "eu-west", false)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := c.AnswerPendingInput(context.Background(), res.Requeued.ID, resp2)
	if err != nil {
		t.Fatalf("second answer failed: %v", err)
	}
	if res2.Result == nil || SummarizeCallToolResult(res2.Result, 0) != "done-after-answer" {
		t.Fatalf("second resume result = %+v", res2.Result)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.retryStates) != 2 || srv.retryStates[0] != "st-1" || srv.retryStates[1] != "st-2" {
		t.Fatalf("retry states = %v, want [st-1 st-2]", srv.retryStates)
	}
}

// TestIssue5699TTLExpiry asserts expired pending answers are swept on access
// and answering an expired entry degrades to a re-issue hint (zz_tasks69
// wording class).
func TestIssue5699TTLExpiry(t *testing.T) {
	srv := pending69Server{}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-ttl")
	c.pendingTTLOverride = 60 * time.Millisecond
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})

	_, err := c.CallTool(context.Background(), "deploy", nil)
	var paErr *PendingAnswerError
	if !errors.As(err, &paErr) {
		t.Fatalf("want defer, got: %v", err)
	}
	if n := c.PendingAnswerCount(); n != 1 {
		t.Fatalf("queue depth = %d, want 1", n)
	}
	time.Sleep(90 * time.Millisecond)
	if n := c.PendingAnswerCount(); n != 0 {
		t.Fatalf("queue depth after TTL = %d, want 0 (swept)", n)
	}
	if got := c.ListPendingAnswers(); len(got) != 0 {
		t.Fatalf("list after TTL = %+v", got)
	}
	_, err = c.AnswerPendingInput(context.Background(), paErr.PA.ID, nil)
	if err == nil || !strings.Contains(err.Error(), "re-issue the tool call") {
		t.Fatalf("want expired/re-issue hint, got: %v", err)
	}
}

// TestIssue5699QueueDepthBound asserts the queue refuses deferrals once
// MaxPendingAnswers live entries exist (no unbounded accumulation, no silent
// eviction of live work).
func TestIssue5699QueueDepthBound(t *testing.T) {
	srv := pending69Server{}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-depth")
	c.pendingTTLOverride = time.Hour // keep entries live for the depth probe
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})
	ctx := context.Background()
	for i := 0; i < MaxPendingAnswers; i++ {
		if _, err := c.CallTool(ctx, "deploy", nil); err == nil {
			t.Fatalf("call %d: expected deferral", i)
		}
	}
	if n := c.PendingAnswerCount(); n != MaxPendingAnswers {
		t.Fatalf("queue depth = %d, want %d", n, MaxPendingAnswers)
	}
	// The next deferral must be refused loudly (not silently queued).
	_, err := c.CallTool(ctx, "deploy", nil)
	if err == nil {
		t.Fatal("expected refusal once queue is full")
	}
	var paErr *PendingAnswerError
	if errors.As(err, &paErr) {
		t.Fatalf("full queue must refuse deferral, not queue: %+v", paErr.PA.ID)
	}
	if !strings.Contains(err.Error(), "deferral refused") || !strings.Contains(err.Error(), "queue full") {
		t.Fatalf("want explicit queue-full wording, got: %v", err)
	}
	if n := c.PendingAnswerCount(); n != MaxPendingAnswers {
		t.Fatalf("queue depth after refusal = %d, want %d", n, MaxPendingAnswers)
	}
}

// TestIssue5699RestartDegradesGracefully defines restart semantics: the
// queue is per-process in-memory; a new client (restart) starts empty and
// answering a lost ID degrades to a not-found/re-issue hint. The server side
// remains governable by its own task TTL.
func TestIssue5699RestartDegradesGracefully(t *testing.T) {
	srv := pending69Server{}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)

	c1 := pending69Client(t, hs, "p69-restart")
	c1.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})
	_, err := c1.CallTool(context.Background(), "deploy", nil)
	var paErr *PendingAnswerError
	if !errors.As(err, &paErr) {
		t.Fatalf("want defer, got: %v", err)
	}
	oldID := paErr.PA.ID
	if err := c1.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulated restart: fresh client against the same server.
	c2 := pending69Client(t, hs, "p69-restart")
	if n := c2.PendingAnswerCount(); n != 0 {
		t.Fatalf("restart must clear the in-memory queue, got %d entries", n)
	}
	_, err = c2.AnswerPendingInput(context.Background(), oldID, nil)
	if err == nil || !strings.Contains(err.Error(), "re-issue the tool call") {
		t.Fatalf("want degraded not-found hint, got: %v", err)
	}
}

// TestIssue5699BuildResponsesCoercion pins the freeform-text mapping for
// single fields, enums, booleans, numbers, multi-field k=v pairs, and the
// --json escape hatch (including unknown-key rejection).
func TestIssue5699BuildResponsesCoercion(t *testing.T) {
	elicit := func(props map[string]any, required ...string) map[string]json.RawMessage {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		raw, _ := json.Marshal(map[string]any{
			"method":          "elicitation/create",
			"message":         "q",
			"requestedSchema": schema,
		})
		return map[string]json.RawMessage{"r1": raw}
	}

	// single string field
	resp, err := BuildPendingAnswerResponses(elicit(map[string]any{"city": map[string]any{"type": "string"}}), "Paris", false)
	if err != nil {
		t.Fatal(err)
	}
	var er ElicitationResult
	if err := json.Unmarshal(resp["r1"], &er); err != nil {
		t.Fatal(err)
	}
	if er.Action != ElicitationActionAccept || er.Content["city"] != "Paris" {
		t.Fatalf("single-field mapping wrong: %+v", er)
	}

	// enum coercion (case-insensitive label match keeps the server's value)
	resp, err = BuildPendingAnswerResponses(elicit(map[string]any{"size": map[string]any{"type": "string", "enum": []any{"small", "large"}}}), "LARGE", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(resp["r1"], &er)
	if er.Content["size"] != "large" {
		t.Fatalf("enum mapping wrong: %+v", er.Content)
	}
	// enum miss lists the options
	_, err = BuildPendingAnswerResponses(elicit(map[string]any{"size": map[string]any{"type": "string", "enum": []any{"small", "large"}}}), "huge", false)
	if err == nil || !strings.Contains(err.Error(), "small, large") {
		t.Fatalf("enum miss must list options, got: %v", err)
	}

	// boolean + integer coercion via k=v pairs
	resp, err = BuildPendingAnswerResponses(elicit(map[string]any{
		"dry":  map[string]any{"type": "boolean"},
		"runs": map[string]any{"type": "integer"},
	}), "dry=yes runs=3", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(resp["r1"], &er)
	if er.Content["dry"] != true || er.Content["runs"] != float64(3) {
		t.Fatalf("k=v coercion wrong: %+v", er.Content)
	}

	// multi-request round requires --json
	twoReq := elicit(map[string]any{"city": map[string]any{"type": "string"}})
	raw2, _ := json.Marshal(map[string]any{"method": "elicitation/create", "message": "q2", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}}})
	twoReq["r2"] = raw2
	_, err = BuildPendingAnswerResponses(twoReq, "text", false)
	if err == nil || !strings.Contains(err.Error(), "--json") {
		t.Fatalf("multi-request must demand --json, got: %v", err)
	}
	// --json with an unknown key is rejected
	_, err = BuildPendingAnswerResponses(twoReq, `{"nope":{"action":"accept"}}`, true)
	if err == nil || !strings.Contains(err.Error(), "not one of the pending requests") {
		t.Fatalf("unknown --json key must be rejected, got: %v", err)
	}
	// --json happy path
	resp, err = BuildPendingAnswerResponses(twoReq, `{"r1":{"action":"accept","content":{"city":"Paris"}},"r2":{"action":"decline"}}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) != 2 {
		t.Fatalf("--json mapping size = %d, want 2", len(resp))
	}

	// sampling maps to an assistant text result
	sampRaw, _ := json.Marshal(map[string]any{"method": "sampling/createMessage", "messages": []any{}})
	resp, err = BuildPendingAnswerResponses(map[string]json.RawMessage{"s1": sampRaw}, "here you go", false)
	if err != nil {
		t.Fatal(err)
	}
	var sr SamplingResult
	if err := json.Unmarshal(resp["s1"], &sr); err != nil {
		t.Fatal(err)
	}
	if sr.Content.Text != "here you go" || sr.Role != "assistant" {
		t.Fatalf("sampling mapping wrong: %+v", sr)
	}
}

// TestIssue5699PendingStatusMessage pins the envelope's human-readable
// status (elicitation message surfaced for /mcp answers).
func TestIssue5699PendingStatusMessage(t *testing.T) {
	srv := pending69Server{}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-msg")
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})
	_, err := c.CallTool(context.Background(), "deploy", nil)
	var paErr *PendingAnswerError
	if !errors.As(err, &paErr) {
		t.Fatalf("want defer, got: %v", err)
	}
	if paErr.PA.StatusMessage != "Which city?" {
		t.Fatalf("status message = %q, want elicitation message", paErr.PA.StatusMessage)
	}
	if paErr.PA.TTL != DefaultPendingAnswerTTL.Milliseconds() {
		t.Fatalf("envelope TTL = %d, want %d", paErr.PA.TTL, DefaultPendingAnswerTTL.Milliseconds())
	}
}

// TestIssue5699TaskEnvelopeResume asserts a resumed call whose retry the
// server converts into a completed task envelope finishes through the
// tasks.go completion path (TaskResult).
func TestIssue5699TaskEnvelopeResume(t *testing.T) {
	srv := pending69Server{script: []string{"task"}}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-task")
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, ErrDeferInputRequest
	})
	_, err := c.CallTool(context.Background(), "deploy", nil)
	var paErr *PendingAnswerError
	if !errors.As(err, &paErr) {
		t.Fatalf("want defer, got: %v", err)
	}
	resp, err := BuildPendingAnswerResponses(paErr.PA.InputRequests, "Paris", false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.AnswerPendingInput(context.Background(), paErr.PA.ID, resp)
	if err != nil {
		t.Fatalf("task-envelope resume failed: %v", err)
	}
	if res.Result == nil || SummarizeCallToolResult(res.Result, 0) != "task-done-after-answer" {
		t.Fatalf("task resume result = %+v", res.Result)
	}
}

// TestIssue5699NonInteractiveCancelKept asserts the ordinary (non-deferred)
// cancel path is untouched: a handler that returns cancel does not queue.
func TestIssue5699NonInteractiveCancelKept(t *testing.T) {
	srv := pending69Server{}
	hs := httptest.NewServer(srv.handle(t))
	t.Cleanup(hs.Close)
	c := pending69Client(t, hs, "p69-cancel")
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return &ElicitationResult{Action: ElicitationActionCancel}, nil
	})
	// Cancel round-trips: the retry carries no responses... server script
	// says complete, so the call completes with the cancel response.
	_, err := c.CallTool(context.Background(), "deploy", nil)
	if err != nil {
		t.Fatalf("cancel-declined call must not fail harder: %v", err)
	}
	if n := c.PendingAnswerCount(); n != 0 {
		t.Fatalf("queue depth after plain cancel = %d, want 0", n)
	}
}
