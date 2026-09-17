package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// sa-52: MCP completion/complete (spec 2025-06-18 "Completion"). Servers
// advertise the plural `completions` capability key; clients request
// argument autocompletion for prompts (ref/prompt) and resource templates
// (ref/resource).

func TestCompletionCapabilityGateAndValidation(t *testing.T) {
	c := NewClient("cap-test", "true", nil)

	c.setNegotiatedState("2025-06-18", ServerCaps{})
	if c.HasCompletion() {
		t.Fatal("HasCompletion = true with empty capabilities")
	}

	_, err := c.Complete(context.Background(), CompleteRequest{Ref: CompleteReference{Name: "deploy"}, Argument: "env"})
	if err == nil || !strings.Contains(err.Error(), "does not advertise completions") {
		t.Fatalf("Complete on uncapable server: got %v, want capability gate error", err)
	}

	c.setNegotiatedState("2025-06-18", ServerCaps{Completions: &struct{}{}})
	if !c.HasCompletion() {
		t.Fatal("HasCompletion = false with completions capability advertised")
	}

	_, err = c.Complete(context.Background(), CompleteRequest{Ref: CompleteReference{}, Argument: "env"})
	if err == nil || !strings.Contains(err.Error(), "requires a prompt name or resource URI") {
		t.Fatalf("Complete with empty reference: got %v, want validation error", err)
	}

	_, err = c.Complete(context.Background(), CompleteRequest{Ref: CompleteReference{Name: "deploy", URI: "file:///tpl/{x}"}, Argument: "env"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("Complete with both reference kinds: got %v, want validation error", err)
	}
}

func TestCompletionWireFormatPromptRef(t *testing.T) {
	testCompletionWireFormat(t, CompleteRequest{
		Ref:         CompleteReference{Name: "deploy"},
		Argument:    "environment",
		Value:       "prod",
		ContextArgs: map[string]string{"region": "us-east-1"},
	}, "ref/prompt")
}

func TestCompletionWireFormatResourceRef(t *testing.T) {
	testCompletionWireFormat(t, CompleteRequest{
		Ref:      CompleteReference{URI: "file:///repos/{owner}/{repo}/readme"},
		Argument: "owner",
		Value:    "top",
	}, "ref/resource")
}

func testCompletionWireFormat(t *testing.T, req CompleteRequest, wantRefType string) {
	t.Helper()

	var mu sync.Mutex
	var capturedParams json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			// The capability key is the plural form `completions`
			// (spec 2025-06-18) - the singular form was a known
			// client-side decode bug this test pins against.
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + latestMCPProtocolVersion + `","capabilities":{"completions":{}},"serverInfo":{"name":"mock","version":"1.0.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "completion/complete":
			mu.Lock()
			capturedParams = json.RawMessage(req.Params)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"completion":{"values":["production","prod-us"],"total":5,"hasMore":true}}}`))
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "completion-mock",
		Type: "http",
		URL:  server.URL,
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !client.HasCompletion() {
		t.Fatal("HasCompletion = false although server advertised plural `completions` capability")
	}

	result, err := client.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Completion.Values) != 2 || result.Completion.Values[0] != "production" {
		t.Fatalf("unexpected completion values: %+v", result.Completion)
	}
	if result.Completion.Total == nil || *result.Completion.Total != 5 {
		t.Fatalf("unexpected total: %+v", result.Completion.Total)
	}
	if !result.Completion.HasMore {
		t.Fatal("HasMore lost in decode")
	}

	mu.Lock()
	params := capturedParams
	mu.Unlock()
	if len(params) == 0 {
		t.Fatal("completion/complete never reached the server")
	}
	var decoded CompleteParams
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("decode captured params %s: %v", params, err)
	}
	if decoded.Ref.Type != wantRefType {
		t.Fatalf("ref.type = %q, want %q", decoded.Ref.Type, wantRefType)
	}
	if decoded.Ref.Name != req.Ref.Name || decoded.Ref.URI != req.Ref.URI {
		t.Fatalf("ref identity mismatch: %+v", decoded.Ref)
	}
	if decoded.Argument.Name != req.Argument || decoded.Argument.Value != req.Value {
		t.Fatalf("argument mismatch: %+v", decoded.Argument)
	}
	if len(req.ContextArgs) > 0 {
		if decoded.Context == nil || decoded.Context.Arguments["region"] != "us-east-1" {
			t.Fatalf("context args not carried: %+v", decoded.Context)
		}
	}
}
