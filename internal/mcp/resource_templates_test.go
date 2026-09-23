package mcp

// Resource template client tests: pagination (#562 pattern), resources
// capability gating, -32601 surfacing for old servers, SEP-2549 cache
// invalidation via notifications/resources/list_changed, and RFC 6570
// expansion. Mirrors zz_issue562_test.go's httptest harness.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestListResourceTemplatesPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		idJSON, _ := json.Marshal(req.ID)
		switch req.Method {
		case "resources/templates/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Cursor == "" {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"resourceTemplates":[{"uriTemplate":"db://tables/{name}","name":"table"}],"nextCursor":"c2"}}`, idJSON)
			} else {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"resourceTemplates":[{"uriTemplate":"db://views/{v}","name":"view"}]}}`, idJSON)
			}
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "tplpag", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.setNegotiatedState(latestMCPProtocolVersion, ServerCaps{Resources: &ResourcesCapability{}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	templates, err := client.ListResourceTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates across pages, got %d: %+v", len(templates), templates)
	}
	if templates[0].URITemplate != "db://tables/{name}" || templates[1].URITemplate != "db://views/{v}" {
		t.Fatalf("unexpected templates: %+v", templates)
	}

	// Cached second call must return the same aggregate.
	again, err := client.ListResourceTemplates(ctx)
	if err != nil || len(again) != 2 {
		t.Fatalf("cached call mismatch: %d templates, err=%v", len(again), err)
	}
}

func TestListResourceTemplatesLegacyServerMethodNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		idJSON, _ := json.Marshal(req.ID)
		if req.Method == "resources/templates/list" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`, idJSON)
			return
		}
		t.Errorf("unexpected method %s", req.Method)
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "tpllegacy", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.setNegotiatedState(latestMCPProtocolVersion, ServerCaps{Resources: &ResourcesCapability{}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.ListResourceTemplates(ctx)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("expected -32601 surface for legacy server, got %v", err)
	}
}

func TestListResourceTemplatesGatedOnResourcesCapability(t *testing.T) {
	client := &Client{name: "nores"}
	// Zero serverCaps → Resources capability nil → no request at all.
	templates, err := client.ListResourceTemplates(context.Background())
	if err != nil {
		t.Fatalf("resources-less server must yield empty list, not error: %v", err)
	}
	if templates != nil {
		t.Fatalf("expected nil templates, got %+v", templates)
	}
}

func TestResourceTemplatesCacheInvalidatedByListChanged(t *testing.T) {
	client := &Client{name: "cachetpl"}
	client.storeListingsCache(cacheResourceTemplates, "", []ResourceTemplate{{URITemplate: "db://x/{y}"}}, []CacheableResult{{TTLms: 60000}})
	if _, ok := client.listingCache.get(cacheResourceTemplates, ""); !ok {
		t.Fatal("cache entry must exist before invalidation")
	}
	client.cacheInvalidateForNotification("notifications/resources/list_changed", nil)
	if _, ok := client.listingCache.get(cacheResourceTemplates, ""); ok {
		t.Fatal("cacheResourceTemplates must be invalidated by resources/list_changed")
	}
}

func TestExpandURITemplate(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		vars map[string]string
		want string
	}{
		{"simple", "db://tables/{name}", map[string]string{"name": "users"}, "db://tables/users"},
		{"pct-encoding", "db://rows/{id}", map[string]string{"id": "a b/c"}, "db://rows/a%20b%2Fc"},
		{"reserved keeps separators", "file:///{+path}", map[string]string{"path": "a/b"}, "file:///a/b"},
		{"multi-value comma join", "db://keys/{k}", map[string]string{"k": "a,b"}, "db://keys/a,b"},
		{"literal passthrough", "scheme://host/{v}/tail", map[string]string{"v": "x"}, "scheme://host/x/tail"},
	}
	for _, tc := range cases {
		got, err := ExpandURITemplate(tc.tmpl, tc.vars)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	errCases := []struct {
		name string
		tmpl string
		vars map[string]string
	}{
		{"missing var", "db://tables/{name}", map[string]string{}},
		{"empty var", "db://tables/{name}", map[string]string{"name": " "}},
		{"unclosed", "db://tables/{name", map[string]string{"name": "x"}},
		{"unsupported operator", "db://search{?q}", map[string]string{"q": "x"}},
		{"unsupported modifier", "db://tables/{name:3}", map[string]string{"name": "x"}},
	}
	for _, tc := range errCases {
		if _, err := ExpandURITemplate(tc.tmpl, tc.vars); err == nil {
			t.Errorf("%s: expected error, got none", tc.name)
		} else if !strings.Contains(err.Error(), tc.tmpl) {
			t.Errorf("%s: error should name the template, got: %v", tc.name, err)
		}
	}
}
