package mcp

// Characterization tests for MCP 2026-07-28 CacheableResult (SEP-2549)
// client-side freshness caching in internal/mcp/client.go + cacheable.go:
//
//	C1: a ttlMs-bearing tools/list result is served from cache on the next
//	    call, and notifications/tools/list_changed invalidates it.
//	C2: a legacy server (no ttlMs) is never cached — behavior unchanged.
//	C3: resources/read entries are cached per-URI; a
//	    notifications/resources/updated refresh drops only that URI.
//	C4: an expired ttlMs hint triggers a refetch.
//	C5: listingCacheTTL unit semantics (min across pages, private
//	    degradation, missing ttl disables).
//	C6: re-initialize (setNegotiatedState) clears the cache.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// newCacheableTestServer starts an httptest MCP server that delegates result
// construction to respond(method). Notifications (no id) get 202.
func newCacheableTestServer(respond func(method string) (any, bool)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || json.Unmarshal(body, &req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			// notifications/initialized and friends: no response body.
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result, ok := respond(req.Method)
		if !ok {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
}

// newCacheableTestClient starts an HTTP client against srv with all listing
// capabilities advertised (mirrors the zz_issue562 pagination harness).
func newCacheableTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	client := NewClientFromConfig(config.MCPServerConfig{Name: "cacheable", Type: "http", URL: srv.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.setNegotiatedState(latestMCPProtocolVersion, ServerCaps{
		Tools:     &ToolsCapability{},
		Prompts:   &PromptsCapability{},
		Resources: &ResourcesCapability{},
	})
	return client
}

// TestCacheableToolsList_TTLHitAndListChangedInvalidation covers C1.
func TestCacheableToolsList_TTLHitAndListChangedInvalidation(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := newCacheableTestServer(func(method string) (any, bool) {
		if method != "tools/list" && method != "initialize" {
			return nil, false
		}
		if method == "initialize" {
			return map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "cacheable", "version": "1"},
			}, true
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		return map[string]any{
			"tools": []map[string]any{{"name": map[bool]string{true: "tool-v1", false: "tool-v2"}[n == 1]}},
			"ttlMs": 60000,
		}, true
	})
	defer srv.Close()
	client := newCacheableTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "tool-v1" {
		t.Fatalf("first ListTools: tools=%v err=%v", tools, err)
	}
	// Second call must be served from the freshness cache — the tool name is
	// still v1 even though the server would now return v2.
	tools, err = client.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "tool-v1" {
		t.Fatalf("cached ListTools: tools=%v err=%v", tools, err)
	}
	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 server call after cache hit, got %d", calls)
	}
	mu.Unlock()

	// The list_changed notification must invalidate the entry; the next call
	// refetches and observes tool-v2.
	client.processNotification(&Notification{
		JSONRPC: "2.0",
		Method:  "notifications/tools/list_changed",
	})
	tools, err = client.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "tool-v2" {
		t.Fatalf("post-invalidate ListTools: tools=%v err=%v", tools, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 server calls after invalidation, got %d", calls)
	}
}

// TestCacheableLegacyServer_NeverCached covers C2: pre-2026-07-28 servers
// send no ttlMs, so every call must reach the server.
func TestCacheableLegacyServer_NeverCached(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := newCacheableTestServer(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "legacy", "version": "1"},
			}, true
		case "tools/list":
			mu.Lock()
			calls++
			mu.Unlock()
			return map[string]any{"tools": []map[string]any{{"name": "t"}}}, true
		}
		return nil, false
	})
	defer srv.Close()
	client := newCacheableTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 2; i++ {
		if _, err := client.ListTools(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("legacy server must not be cached: got %d calls, want 2", calls)
	}
}

// TestCacheableResourceRead_PerURICacheAndUpdatedInvalidation covers C3.
func TestCacheableResourceRead_PerURICacheAndUpdatedInvalidation(t *testing.T) {
	var mu sync.Mutex
	perURI := map[string]int{}
	srv := newCacheableTestServer(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "res", "version": "1"},
			}, true
		case "resources/read":
			var req struct {
				Params struct {
					URI string `json:"uri"`
				} `json:"params"`
			}
			// The handler closure cannot see the body; re-reads are handled by
			// counting per URI inside ListResources-style wrappers below.
			_ = req
			return nil, false
		}
		return nil, false
	})
	// Per-URI counting needs the request body; build a dedicated server that
	// decodes params instead of using the shared helper.
	srv.Close()
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				URI string `json:"uri"`
			} `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		if json.Unmarshal(body, &req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "res", "version": "1"},
			}
		case "resources/read":
			mu.Lock()
			perURI[req.Params.URI]++
			mu.Unlock()
			result = map[string]any{
				"contents": []map[string]any{{"uri": req.Params.URI, "text": "content"}},
				"ttlMs":    60000,
			}
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}))
	defer srv.Close()
	client := newCacheableTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 2; i++ {
		res, err := client.ReadResource(ctx, "file:///a")
		if err != nil || len(res.Contents) != 1 {
			t.Fatalf("ReadResource a: %+v err=%v", res, err)
		}
	}
	if _, err := client.ReadResource(ctx, "file:///b"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if perURI["file:///a"] != 1 || perURI["file:///b"] != 1 {
		mu.Unlock()
		t.Fatalf("per-URI cache miss: %v", perURI)
	}
	mu.Unlock()

	// resources/updated for /a drops only /a's entry.
	client.processNotification(&Notification{
		JSONRPC: "2.0",
		Method:  "notifications/resources/updated",
		Params:  json.RawMessage(`{"uri":"file:///a"}`),
	})
	if _, err := client.ReadResource(ctx, "file:///a"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadResource(ctx, "file:///b"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if perURI["file:///a"] != 2 {
		t.Fatalf("uri /a must refetch after resources/updated: %v", perURI)
	}
	if perURI["file:///b"] != 1 {
		t.Fatalf("uri /b must stay cached: %v", perURI)
	}
}

// TestCacheableExpiry_RefetchAfterTTL covers C4.
func TestCacheableExpiry_RefetchAfterTTL(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := newCacheableTestServer(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "expiry", "version": "1"},
			}, true
		case "tools/list":
			mu.Lock()
			calls++
			mu.Unlock()
			return map[string]any{"tools": []map[string]any{{"name": "t"}}, "ttlMs": 25}, true
		}
		return nil, false
	})
	defer srv.Close()
	client := newCacheableTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond) // > ttlMs=25
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expired entry must refetch: got %d calls, want 2", calls)
	}
}

// TestListingCacheTTL_Unit covers C5.
func TestListingCacheTTL_Unit(t *testing.T) {
	// Minimum across pages wins.
	ttl, scope, ok := listingCacheTTL([]CacheableResult{
		{TTLms: 60000, CacheScope: "public"},
		{TTLms: 30000, CacheScope: "public"},
	})
	if !ok || ttl != 30*time.Second || scope != "public" {
		t.Fatalf("min ttl: ttl=%v scope=%q ok=%v", ttl, scope, ok)
	}
	// Any private page degrades the scope.
	_, scope, ok = listingCacheTTL([]CacheableResult{
		{TTLms: 60000, CacheScope: "public"},
		{TTLms: 60000, CacheScope: "private"},
	})
	if !ok || scope != "private" {
		t.Fatalf("private degradation: scope=%q ok=%v", scope, ok)
	}
	// A missing ttl (legacy page) disables caching entirely.
	if _, _, ok := listingCacheTTL([]CacheableResult{{TTLms: 60000}, {}}); ok {
		t.Fatal("page without ttlMs must disable caching")
	}
	// No pages at all — nothing cacheable.
	if _, _, ok := listingCacheTTL(nil); ok {
		t.Fatal("empty pages must not be cacheable")
	}
}

// TestCacheableReinitialize_Invalidates covers C6.
func TestCacheableReinitialize_Invalidates(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := newCacheableTestServer(func(method string) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "reinit", "version": "1"},
			}, true
		case "tools/list":
			mu.Lock()
			calls++
			mu.Unlock()
			return map[string]any{"tools": []map[string]any{{"name": "t"}}, "ttlMs": 60000}, true
		}
		return nil, false
	})
	defer srv.Close()
	client := newCacheableTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	// Simulate a reconnect/re-initialize: capabilities are re-negotiated and
	// every cached freshness hint from the old session must be dropped.
	client.setNegotiatedState(latestMCPProtocolVersion, ServerCaps{Tools: &ToolsCapability{}})
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("re-initialize must invalidate cache: got %d calls, want 2", calls)
	}
}
