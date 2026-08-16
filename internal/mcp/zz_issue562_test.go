package mcp

// Characterization tests for issue #562 (internal/mcp/client.go):
//
//	Bug B (HIGH, reproduced panic): sendWSNotification wrote to the gorilla
//	         connection without holding c.mu — a concurrent request write
//	         panics with "concurrent write to websocket connection".
//	Bug C (HIGH): sampling/elicitation handlers ran inside the stdio read
//	         loop while readMu was held; a slow user response stalled every
//	         concurrent request until timeout/Abort.
//	Bug A (MEDIUM): List*/ methods ignored nextCursor — paginated servers
//	         only ever yielded page 1.
//	Bug E (MEDIUM): ListTools returned an error when the server advertised
//	         no tools capability, failing whole-server discovery.
//	Bug F (TSan): Initialize's serverCaps/negotiatedVersion writes raced
//	         the capability readers.
//	Bug D: id:null JSON-RPC error responses were misattributed to whichever
//	         waiter held the read loop, or silently dropped.
//	Bug G: a failed initialized notification was reported as an initialize
//	         failure even though the handshake had already succeeded.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/config"
)

// --- Bug B: WS notification concurrent write ---

// TestIssue562B_WSNotificationConcurrentWriteNoPanic drives concurrent
// sendNotification + sendRequest traffic over one WS connection while the
// server echoes. Before the fix, gorilla's single-writer assertion panicked
// the test process. The server delays responses so the notification write
// overlaps an in-flight request write window.
func TestIssue562B_WSNotificationConcurrentWriteNoPanic(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req Request
			if err := json.Unmarshal(payload, &req); err != nil {
				continue
			}
			switch req.Method {
			case "tools/list":
				// Delay the response so the caller's read loop stays parked
				// while notifications stream in — maximizes write overlap.
				time.Sleep(30 * time.Millisecond)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"tools": []any{}},
				}
				data, _ := json.Marshal(resp)
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					return
				}
			default:
				// Notifications: no reply needed.
			}
		}
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client := NewClientFromConfig(config.MCPServerConfig{Name: "ws562", Type: "ws", URL: wsURL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				// Interleave notifications (sendWSNotification) with requests
				// (sendWSUnlocked) — the exact pair that raced before #562 B.
				notif := Notification{JSONRPC: "2.0", Method: "notifications/initialized"}
				if err := client.sendNotification(context.Background(), notif); err != nil {
					errCh <- fmt.Errorf("notification: %w", err)
					return
				}
				if _, err := client.ListTools(context.Background()); err != nil {
					errCh <- fmt.Errorf("listTools: %w", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// --- Bug C: elicitation must not block the read loop ---

// TestIssue562C_ElicitationDoesNotBlockReadLoop feeds a stdio stream where
// an elicitation/create request (handler blocks on a gate we control) is
// followed by a normal response for a concurrent request. Before the fix,
// readResponse parked inside the elicitation handler while holding readMu,
// so the trailing response could never be consumed until the 5-minute
// handler timeout elapsed. With the fix, the read loop keeps consuming.
func TestIssue562C_ElicitationDoesNotBlockReadLoop(t *testing.T) {
	elicitID := NewIntID(100)
	respFor1 := Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)}
	stream := encodeStdioMessages(t,
		Request{
			JSONRPC: "2.0",
			Method:  "elicitation/create",
			ID:      &elicitID,
			Params:  json.RawMessage(`{"message":"hi","requestedSchema":{"type":"object","properties":{"a":{"type":"string"}}}}`),
		},
		respFor1,
	)

	// Blocking handler: released only after the main test signals.
	gate := make(chan struct{})
	var handlerDone sync.WaitGroup
	handlerDone.Add(1)
	client := &Client{
		name:   "stdio562c",
		reader: bufio.NewReader(bytes.NewReader(stream)),
		stdin:  nopWriteCloser{Writer: &bytes.Buffer{}},
	}
	client.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		defer handlerDone.Done()
		select {
		case <-gate:
			return &ElicitationResult{Action: ElicitationActionDecline}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	// Consume the stream via the read loop as a request waiter would.
	waiter := make(chan *Response, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.readResponse(ctx, nil, waiter)
	if err != nil {
		t.Fatalf("readResponse blocked or failed (expected response to be consumed while elicitation handler is parked): %v", err)
	}
	if resp == nil || string(resp.Result) != `{"ok":true}` {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Release the elicitation handler and wait for it to finish on its
	// worker goroutine.
	close(gate)
	handlerDoneDone := make(chan struct{})
	go func() { handlerDone.Wait(); close(handlerDoneDone) }()
	select {
	case <-handlerDoneDone:
	case <-time.After(3 * time.Second):
		t.Fatal("elicitation handler never ran — dispatch was not deferred off the read loop")
	}
}

// --- Bug A: pagination ---

// TestIssue562A_ListToolsFollowsNextCursor uses an HTTP MCP server that
// returns two pages of tools via nextCursor. The client must return all 3
// tools; before the fix only page 1's single tool came back.
func TestIssue562A_ListToolsFollowsNextCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/list":
			var params struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.Cursor == "" {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"t1","inputSchema":{"type":"object"}}],"nextCursor":"page2"}}`)
				return
			}
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"t2","inputSchema":{"type":"object"}},{"name":"t3","inputSchema":{"type":"object"}}]}}`)
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "pag", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// Advertise tools capability so the Bug E gate passes.
	client.setNegotiatedState(latestMCPProtocolVersion, ServerCaps{Tools: &ToolsCapability{}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools across 2 pages, got %d: %+v", len(tools), tools)
	}
}

// TestIssue562A_ListPromptsAndResourcesPagination verifies the same
// cursor-following for prompts/list and resources/list.
func TestIssue562A_ListPromptsAndResourcesPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "prompts/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Cursor == "" {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"prompts":[{"name":"p1"}],"nextCursor":"c2"}}`)
			} else {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"prompts":[{"name":"p2"}]}}`)
			}
		case "resources/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Cursor == "" {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":3,"result":{"resources":[{"uri":"file:///a"}],"nextCursor":"c2"}}`)
			} else {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":4,"result":{"resources":[{"uri":"file:///b"}]}}`)
			}
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "pag2", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts across pages, got %d", len(prompts))
	}
	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources across pages, got %d", len(resources))
	}
}

// --- Bug E: tools capability gating ---

// TestIssue562E_ListToolsEmptyWhenCapabilityMissing: a resources-only server
// must get an empty list (not an error) from ListTools so that production
// discoverCapabilities doesn't fail the whole server.
func TestIssue562E_ListToolsEmptyWhenCapabilityMissing(t *testing.T) {
	client := &Client{name: "resOnly"}
	// serverCaps zero value → Tools == nil.
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools must not error on tools-less servers: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected empty tools, got %d", len(tools))
	}
}

// --- Bug F: caps state synchronization ---

// TestIssue562F_CapsStateConcurrentAccess hammers capability readers while
// a goroutine re-runs the negotiated-state update (reconnect re-entry).
// Under -race, the pre-fix unsynchronized field writes are reported.
func TestIssue562F_CapsStateConcurrentAccess(t *testing.T) {
	client := &Client{name: "race562"}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				client.HasToolsListChanged()
				client.HasLogging()
				client.HasResourceSubscribe()
				_ = client.NegotiatedVersion()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				client.setNegotiatedState("2025-06-18", ServerCaps{
					Tools:     &ToolsCapability{ListChanged: true},
					Logging:   &struct{}{},
					Resources: &ResourcesCapability{Subscribe: true},
					Prompts:   &PromptsCapability{},
				})
			}
		}
	}()
	// Third goroutine exercises the same read path from sendRequest gating.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = client.negotiatedState()
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// --- Bug D: id:null responses ---

// TestIssue562D_NullIDErrorResponseNotMisattributed: an id:null error
// response in the stream must be skipped (logged) and not returned as the
// answer to the pending request, and must not deadlock the read loop.
func TestIssue562D_NullIDErrorResponseNotMisattributed(t *testing.T) {
	nullResp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`null`),
		Error:   &Error{Code: -32700, Message: "Parse error"},
	}
	respFor1 := Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)}
	stream := encodeStdioMessages(t, nullResp, respFor1)
	client := &Client{
		name:   "stdio562d",
		reader: bufio.NewReader(bytes.NewReader(stream)),
	}
	resp, err := client.readResponse(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.ID) != "1" {
		t.Fatalf("expected the id:1 response, got id %s (null-ID response was misattributed)", resp.ID)
	}
}

// TestIssue562D_NullIDResponseSkippedInWSLoop: same rule on the WS read
// loop via deliverResponse's isNullID path.
func TestIssue562D_NullIDResponseSkippedInWSLoop(t *testing.T) {
	resp := &Response{ID: json.RawMessage(`null`), Error: &Error{Code: -32700, Message: "x"}}
	// Must not panic and must not deliver anywhere.
	c := &Client{name: "ws562d"}
	c.deliverResponse(resp) // exercised for log-path coverage

	nullResp := Response{JSONRPC: "2.0", ID: json.RawMessage(` null `), Error: &Error{Code: -32700, Message: "Parse error"}}
	if !isNullID(nullResp.ID) {
		t.Fatal("isNullID must accept whitespace-padded null")
	}
	empty := Response{JSONRPC: "2.0", ID: json.RawMessage(``)}
	if !isNullID(empty.ID) {
		t.Fatal("isNullID must accept empty ID")
	}
	normal := Response{JSONRPC: "2.0", ID: json.RawMessage(`5`)}
	if isNullID(normal.ID) {
		t.Fatal("isNullID must reject integer IDs")
	}
}

// --- Bug G: initialized notification failure downgraded ---

// TestIssue562G_InitializeSurvivesInitializedNotifyFailure: the HTTP server
// accepts initialize but returns 500 for notifications/initialized. The
// handshake result must still be returned (failure downgraded to a debug
// log), not surfaced as an initialize error.
func TestIssue562G_InitializeSurvivesInitializedNotifyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "initialize":
			idJSON, _ := json.Marshal(req.ID)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"%s","capabilities":{"resources":{}},"serverInfo":{"name":"mock","version":"1.0"}}}`, idJSON, latestMCPProtocolVersion)
		case "notifications/initialized":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "g562", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize must succeed when only the initialized notification fails: %v", err)
	}
	if result == nil || result.ServerInfo.Name != "mock" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if client.NegotiatedVersion() != latestMCPProtocolVersion {
		t.Fatalf("expected negotiated version recorded, got %q", client.NegotiatedVersion())
	}
}
