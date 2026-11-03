package mcp

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

	close(gate)
	handlerDoneDone := make(chan struct{})
	go func() { handlerDone.Wait(); close(handlerDoneDone) }()
	select {
	case <-handlerDoneDone:
	case <-time.After(3 * time.Second):
		t.Fatal("elicitation handler never ran — dispatch was not deferred off the read loop")
	}
}

func TestIssue562A_ListToolsFollowsNextCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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

func TestIssue562A_ListPromptsAndResourcesPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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

func TestIssue562E_ListToolsEmptyWhenCapabilityMissing(t *testing.T) {
	client := &Client{name: "resOnly"}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools must not error on tools-less servers: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected empty tools, got %d", len(tools))
	}
}

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

func TestIssue562D_NullIDResponseSkippedInWSLoop(t *testing.T) {
	resp := &Response{ID: json.RawMessage(`null`), Error: &Error{Code: -32700, Message: "x"}}

	c := &Client{name: "ws562d"}
	c.deliverResponse(resp)

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

func TestIssue562G_InitializeSurvivesInitializedNotifyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
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
