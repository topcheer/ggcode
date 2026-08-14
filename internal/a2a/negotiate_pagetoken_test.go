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

// pagetokenTestPost performs an authenticated JSON-RPC POST (testPost in
// a2a_test.go is behind the integration_local build tag).
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

// TestClientNegotiateAuthAPIKeyFallbackRejected (fix #257): a client built
// with NewClient(url, "some-key") pointing at a server that only declares
// oauth2/bearer schemes must NOT silently negotiate apiKey success — the old
// fallback deferred the failure to the first request's 401.
func TestClientNegotiateAuthAPIKeyFallbackRejected(t *testing.T) {
	client := NewClient("http://example.com", "some-key")
	client.card = &AgentCard{
		SecuritySchemes: map[string]Security{
			"oauth2": {Type: "oauth2"},
		},
		Security: []map[string][]string{
			{"oauth2": {}},
		},
	}
	err := client.NegotiateAuth()
	if err == nil {
		t.Fatal("expected negotiation error for apiKey client vs bearer-only server")
	}
	if !strings.Contains(err.Error(), "apiKey") {
		t.Errorf("error should mention apiKey mismatch, got: %v", err)
	}
}

// TestClientNegotiateAuthAPIKeyFallbackBearerOnly verifies the same fix
// against a server declaring only an http bearer scheme.
func TestClientNegotiateAuthAPIKeyFallbackBearerOnly(t *testing.T) {
	client := NewClient("http://example.com", "some-key")
	client.card = &AgentCard{
		SecuritySchemes: map[string]Security{
			"bearer": {Type: "http"},
		},
		Security: []map[string][]string{
			{"bearer": {}},
		},
	}
	if err := client.NegotiateAuth(); err == nil {
		t.Fatal("expected negotiation error for apiKey client vs bearer-only server")
	}
}

// TestClientNegotiateAuthAPIKeyStillOKWhenDeclared: when the server DOES
// declare an apiKey scheme, the pre-configured apiKey fallback stays valid.
func TestClientNegotiateAuthAPIKeyStillOKWhenDeclared(t *testing.T) {
	client := NewClient("http://example.com", "some-key")
	client.card = &AgentCard{
		SecuritySchemes: map[string]Security{
			"apiKeyScheme": {Type: "apiKey", Location: "header", Name: "X-API-Key"},
		},
		Security: []map[string][]string{
			{"apiKeyScheme": {}},
		},
	}
	if err := client.NegotiateAuth(); err != nil {
		t.Fatalf("expected apiKey fallback to be accepted, got: %v", err)
	}
	if client.AuthMethod() != "apiKey" {
		t.Errorf("expected apiKey, got %q", client.AuthMethod())
	}
}

// TestListTasksInvalidPageToken (fix #258): a stale pageToken (task deleted
// by cleanup) must surface an error instead of silently returning page one
// with an identical nextToken, which looped pagination clients forever.
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

// TestListTasksRPCInvalidPageToken verifies the JSON-RPC layer maps the stale
// token error to invalid params (-32602) rather than a first-page result.
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
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	resp.Body.Close()
	if rpcResp.Error == nil {
		t.Fatal("expected JSON-RPC error for stale page token, got success")
	}
	if rpcResp.Error.Code != -32602 {
		t.Errorf("expected -32602 invalid params, got %d (%s)", rpcResp.Error.Code, rpcResp.Error.Message)
	}
}
