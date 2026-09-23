package mcp

// sa-144: transport-level coverage for the SEP-1686 task client methods
// (CallToolAsTask / GetTask / TaskResult / CancelTask / ListTasks / HasTasks).
// These tests drive a real JSON-RPC over HTTP round trip against a fake MCP
// server (httptest), matching the MCP 2026 protocol-conformance testing
// direction: the client is exercised through the wire, not via stubbed
// internals.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// sa144FakeServer records per-method invocation counts and delegates unknown
// methods to the test-provided handler.
type sa144FakeServer struct {
	mu     sync.Mutex
	counts map[string]int
	handle func(method string, params json.RawMessage) (result any, handled bool)
}

func (f *sa144FakeServer) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[method]
}

func sa144RPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]interface{}{"code": code, "message": msg},
	}
	payload, _ := json.Marshal(resp)
	_, _ = w.Write(append(append([]byte("data: "), payload...), '\n', '\n'))
}

// sa144NewServer starts a fake MCP HTTP server that completes the initialize
// handshake (advertising the tasks capability when requested) and routes
// everything else through handle.
func sa144NewServer(t *testing.T, tasksCap bool, handle func(method string, params json.RawMessage) (any, bool)) (*httptest.Server, *sa144FakeServer) {
	t.Helper()
	fake := &sa144FakeServer{counts: map[string]int{}, handle: handle}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req mcpTestServerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		fake.mu.Lock()
		fake.counts[req.Method]++
		fake.mu.Unlock()
		switch req.Method {
		case "initialize":
			caps := map[string]any{}
			if tasksCap {
				caps["tasks"] = map[string]any{"list": map[string]any{}, "cancel": map[string]any{}}
			}
			sseResult(w, req.ID, map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    caps,
				"serverInfo":      map[string]any{"name": "sa144-fake", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		default:
			if fake.handle != nil {
				if res, handled := fake.handle(req.Method, req.Params); handled {
					sseResult(w, req.ID, res)
					return
				}
			}
			sa144RPCError(w, req.ID, -32601, "method not found: "+req.Method)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fake
}

func sa144StartClient(t *testing.T, url string) *Client {
	t.Helper()
	c := NewClientFromConfig(config.MCPServerConfig{Name: "sa144", Type: "http", URL: url})
	if c == nil {
		t.Fatal("NewClientFromConfig returned nil")
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	c.taskPollOverride = time.Millisecond
	return c
}

func sa144TaskEnvelope(taskID, status string) map[string]any {
	return map[string]any{"resultType": "task", "taskId": taskID, "status": status}
}

func TestSa144CallToolAsTaskLifecycle(t *testing.T) {
	srv, fake := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		switch method {
		case "tools/call":
			env := sa144TaskEnvelope("t-1", TaskStatusWorking)
			env["pollInterval"] = 1
			return env, true
		case "tasks/get":
			return map[string]any{"taskId": "t-1", "status": TaskStatusCompleted}, true
		case "tasks/result":
			return map[string]any{
				"content": []map[string]string{{"type": "text", "text": "done"}},
			}, true
		}
		return nil, false
	})
	c := sa144StartClient(t, srv.URL)
	res, task, err := c.CallToolAsTask(context.Background(), "slow", map[string]any{"x": 1}, json.RawMessage(`true`))
	if err != nil {
		t.Fatalf("CallToolAsTask: %v", err)
	}
	if task == nil || task.TaskID != "t-1" || task.Status != TaskStatusCompleted {
		t.Fatalf("unexpected terminal task: %+v", task)
	}
	if res == nil || len(res.Content) == 0 || res.Content[0].Text != "done" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := fake.count("tasks/get"); got != 1 {
		t.Fatalf("expected exactly 1 tasks/get poll, got %d", got)
	}
}

func TestSa144CallToolAsTaskSyncPlainResult(t *testing.T) {
	srv, _ := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		if method == "tools/call" {
			return map[string]any{
				"content": []map[string]string{{"type": "text", "text": "sync-ok"}},
			}, true
		}
		return nil, false
	})
	c := sa144StartClient(t, srv.URL)
	res, task, err := c.CallToolAsTask(context.Background(), "quick", nil, json.RawMessage(`{"ttl":60000}`))
	if err != nil {
		t.Fatalf("CallToolAsTask: %v", err)
	}
	if task != nil {
		t.Fatalf("expected nil task for synchronous answer, got %+v", task)
	}
	if res == nil || len(res.Content) == 0 || res.Content[0].Text != "sync-ok" {
		t.Fatalf("unexpected plain result: %+v", res)
	}
}

func TestSa144CallToolAsTaskTerminalFailures(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		msg     string
		wantErr string
	}{
		{"failed", TaskStatusFailed, "boom", "failed"},
		{"cancelled", TaskStatusCancelled, "", "cancelled"},
		{"unknown-status", "zombie", "", "unknown status"},
		{"input-required", TaskStatusInputRequired, "need answer", "input_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
				switch method {
				case "tools/call":
					return sa144TaskEnvelope("t-err", TaskStatusWorking), true
				case "tasks/get":
					return map[string]any{"taskId": "t-err", "status": tc.status, "statusMessage": tc.msg}, true
				}
				return nil, false
			})
			c := sa144StartClient(t, srv.URL)
			res, task, err := c.CallToolAsTask(context.Background(), "x", nil, json.RawMessage(`true`))
			if err == nil {
				t.Fatalf("expected error for status %q", tc.status)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
			if res != nil {
				t.Fatalf("expected nil result, got %+v", res)
			}
			if task == nil || task.Status != tc.status {
				t.Fatalf("expected terminal task descriptor, got %+v", task)
			}
		})
	}
}

func TestSa144CallToolAsTaskMissingTaskID(t *testing.T) {
	srv, _ := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		if method == "tools/call" {
			// Flat envelope requires taskId; a bare discriminator without one
			// is a protocol violation.
			return map[string]any{"resultType": "task", "status": TaskStatusWorking}, true
		}
		return nil, false
	})
	c := sa144StartClient(t, srv.URL)
	_, _, err := c.CallToolAsTask(context.Background(), "x", nil, json.RawMessage(`true`))
	if err == nil || !strings.Contains(err.Error(), "without taskId") {
		t.Fatalf("expected missing-taskId error, got %v", err)
	}
}

func TestSa144CallToolAsTaskResultFetchError(t *testing.T) {
	srv, _ := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		switch method {
		case "tools/call":
			return sa144TaskEnvelope("t-res", TaskStatusWorking), true
		case "tasks/get":
			return map[string]any{"taskId": "t-res", "status": TaskStatusCompleted}, true
		}
		// tasks/result falls through to method-not-found (JSON-RPC error).
		return nil, false
	})
	c := sa144StartClient(t, srv.URL)
	_, _, err := c.CallToolAsTask(context.Background(), "x", nil, json.RawMessage(`true`))
	if err == nil || !strings.Contains(err.Error(), "tasks/result") {
		t.Fatalf("expected tasks/result transport error, got %v", err)
	}
}

func TestSa144CallToolAsTaskClosedClient(t *testing.T) {
	srv, _ := sa144NewServer(t, true, nil)
	c := sa144StartClient(t, srv.URL)
	c.Close()
	_, _, err := c.CallToolAsTask(context.Background(), "x", nil, json.RawMessage(`true`))
	if err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("expected closed-connection error, got %v", err)
	}
}

func TestSa144GetTaskBackfillsTaskID(t *testing.T) {
	srv, _ := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		if method == "tasks/get" {
			// Server omits taskId; the client must echo the requested id.
			return map[string]any{"status": TaskStatusWorking, "statusMessage": "grinding"}, true
		}
		return nil, false
	})
	c := sa144StartClient(t, srv.URL)
	task, err := c.GetTask(context.Background(), "t-9")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.TaskID != "t-9" || task.Status != TaskStatusWorking {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestSa144CancelTask(t *testing.T) {
	srv, _ := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		if method == "tasks/cancel" {
			return map[string]any{"status": TaskStatusWorking}, true
		}
		return nil, false
	})
	c := sa144StartClient(t, srv.URL)
	task, err := c.CancelTask(context.Background(), "t-c")
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if task.TaskID != "t-c" || task.Status != TaskStatusWorking {
		t.Fatalf("cancellation is best-effort; unexpected descriptor: %+v", task)
	}
}

func TestSa144TaskResultError(t *testing.T) {
	srv, _ := sa144NewServer(t, true, nil) // tasks/result → method not found
	c := sa144StartClient(t, srv.URL)
	if _, err := c.TaskResult(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "tasks/result") {
		t.Fatalf("expected tasks/result error, got %v", err)
	}
}

func TestSa144ListTasksPagination(t *testing.T) {
	srv, fake := sa144NewServer(t, true, func(method string, params json.RawMessage) (any, bool) {
		if method != "tasks/list" {
			return nil, false
		}
		var p ListToolsParams
		_ = json.Unmarshal(params, &p)
		if p.Cursor == "" {
			return map[string]any{
				"tasks":      []map[string]string{{"taskId": "a", "status": TaskStatusWorking}},
				"nextCursor": "page-2",
			}, true
		}
		return map[string]any{
			"tasks": []map[string]string{{"taskId": "b", "status": TaskStatusCompleted}},
		}, true
	})
	c := sa144StartClient(t, srv.URL)
	tasks, err := c.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 || tasks[0].TaskID != "a" || tasks[1].TaskID != "b" {
		t.Fatalf("unexpected task list: %+v", tasks)
	}
	if got := fake.count("tasks/list"); got != 2 {
		t.Fatalf("expected 2 pages, got %d", got)
	}
}

func TestSa144ListTasksLifecycleGuards(t *testing.T) {
	// No tasks capability advertised: HasTasks false, ListTasks returns an
	// empty (non-nil) list without touching the wire.
	srv, _ := sa144NewServer(t, false, nil)
	c := sa144StartClient(t, srv.URL)
	if c.HasTasks() {
		t.Fatal("HasTasks must be false when capability absent")
	}
	tasks, err := c.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks without capability: %v", err)
	}
	if tasks == nil || len(tasks) != 0 {
		t.Fatalf("expected empty non-nil list, got %+v", tasks)
	}

	// With the capability advertised HasTasks flips true.
	srv2, _ := sa144NewServer(t, true, nil)
	c2 := sa144StartClient(t, srv2.URL)
	if !c2.HasTasks() {
		t.Fatal("HasTasks must be true when capability advertised")
	}

	// Closed client refuses ListTasks outright.
	c2.Close()
	if _, err := c2.ListTasks(context.Background()); err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("expected closed-connection error, got %v", err)
	}
}
