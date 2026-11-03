package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func pagetokenTestPost(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}

func TestClientNegotiateAuthAPIKeyFallbackRejected(t *testing.T) {
	client := NewClient("http://example.com", "some-key")
	client.card.Store(&AgentCard{
		SecuritySchemes: map[string]Security{
			"oauth2": {Type: "oauth2"},
		},
		Security: []map[string][]string{
			{"oauth2": {}},
		},
	})
	err := client.NegotiateAuth()
	if err == nil {
		t.Fatal("expected negotiation error for apiKey client vs bearer-only server")
	}
	if !strings.Contains(err.Error(), "apiKey") {
		t.Errorf("error should mention apiKey mismatch, got: %v", err)
	}
}

func TestClientNegotiateAuthAPIKeyFallbackBearerOnly(t *testing.T) {
	client := NewClient("http://example.com", "some-key")
	client.card.Store(&AgentCard{
		SecuritySchemes: map[string]Security{
			"bearer": {Type: "http"},
		},
		Security: []map[string][]string{
			{"bearer": {}},
		},
	})
	if err := client.NegotiateAuth(); err == nil {
		t.Fatal("expected negotiation error for apiKey client vs bearer-only server")
	}
}

func TestClientNegotiateAuthAPIKeyStillOKWhenDeclared(t *testing.T) {
	client := NewClient("http://example.com", "some-key")
	client.card.Store(&AgentCard{
		SecuritySchemes: map[string]Security{
			"apiKeyScheme": {Type: "apiKey", Location: "header", Name: "X-API-Key"},
		},
		Security: []map[string][]string{
			{"apiKeyScheme": {}},
		},
	})
	if err := client.NegotiateAuth(); err != nil {
		t.Fatalf("expected apiKey fallback to be accepted, got: %v", err)
	}
	if client.AuthMethod() != "apiKey" {
		t.Errorf("expected apiKey, got %q", client.AuthMethod())
	}
}

func TestListTasksInvalidPageToken(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	_, next, err := handler.ListTasks("ghost-token", 10)
	if err == nil {
		t.Fatal("expected error for stale page token")
	}
	if next != "" {
		t.Errorf("expected empty nextToken on error, got %q", next)
	}
}

func TestListTasksRPCInvalidPageToken(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "test-key"}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	base := "http://127.0.0.1:" + fmt.Sprintf("%d", srv.Port())
	body, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "tasks/list",
		Params:  json.RawMessage(`{"pageToken":"ghost-token"}`),
	})
	resp, err := pagetokenTestPost(base, body)
	if err != nil {
		t.Fatal(err)
	}
	var rpcResp JSONRPCResponse
	if derr := json.NewDecoder(resp.Body).Decode(&rpcResp); derr != nil {
		t.Fatalf("decoding JSON-RPC response: %v", derr)
	}
	resp.Body.Close()
	if rpcResp.Error == nil {
		t.Fatal("expected JSON-RPC error for stale page token, got success")
	}
	if rpcResp.Error.Code != -32602 {
		t.Errorf("expected -32602 invalid params, got %d (%s)", rpcResp.Error.Code, rpcResp.Error.Message)
	}
}
