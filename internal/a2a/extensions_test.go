package a2a

// Tests for A2A v1.0 protocol extension negotiation:
//   - Agent Card declaration (capabilities.extensions + legacy flat field)
//   - A2A-Extensions request/response header activation
//   - required:true extension gating (-32004 UnsupportedOperation)
//   - client-side activation opt-in and required-extension pre-flight

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	testExtURI        = "https://example.com/ext/timestamp/v1"
	testExtReqURI     = "https://example.com/ext/signed-message/v1"
	testExtUnknownURI = "https://example.com/ext/unknown/v1"
)

func TestParseFormatA2AExtensionsHeader(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{testExtURI, []string{testExtURI}},
		{testExtURI + ", " + testExtReqURI, []string{testExtURI, testExtReqURI}},
		{testExtURI + " " + testExtReqURI, []string{testExtURI, testExtReqURI}}, // space-separated
		{testExtURI + ",," + testExtReqURI + ",", []string{testExtURI, testExtReqURI}},
		{testExtURI + "," + testExtURI, []string{testExtURI}}, // dedup, order preserved
	}
	for _, tc := range cases {
		got := ParseA2AExtensionsHeader(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("ParseA2AExtensionsHeader(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("ParseA2AExtensionsHeader(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
	if got := FormatA2AExtensionsHeader([]string{testExtURI, testExtReqURI, testExtURI}); got != testExtURI+", "+testExtReqURI {
		t.Fatalf("FormatA2AExtensionsHeader dedup failed: %q", got)
	}
	if got := FormatA2AExtensionsHeader(nil); got != "" {
		t.Fatalf("FormatA2AExtensionsHeader(nil) = %q", got)
	}
}

// startExtTestServer builds a minimal A2A server with the given extensions.
func startExtTestServer(t *testing.T, exts []AgentExtension) *Server {
	t.Helper()
	srv := NewServer(ServerConfig{
		Host:       "127.0.0.1",
		Port:       0,
		Extensions: exts,
	}, NewTaskHandler(t.TempDir(), nil, nil))
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func extTestRPC(t *testing.T, endpoint string, hdr http.Header) (int, http.Header, *JSONRPCResponse) {
	t.Helper()
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tasks/list","params":{}}`)
	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var rpc JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, resp.Header, &rpc
}

func TestServerExtensionActivationEcho(t *testing.T) {
	srv := startExtTestServer(t, []AgentExtension{{URI: testExtURI, Description: "ts"}})

	// Without activation: request proceeds, no echo header (nothing activated).
	_, hdr, rpc := extTestRPC(t, srv.Endpoint(), nil)
	if v := hdr.Get(A2AExtensionsHeader); v != "" {
		t.Fatalf("unexpected %s echo without activation: %q", A2AExtensionsHeader, v)
	}
	if rpc.Error != nil && rpc.Error.Code == ErrUnsupportedOp.Code {
		t.Fatalf("optional extension must not gate: %+v", rpc.Error)
	}

	// With activation (plus an unsupported URI the agent ignores): echoed set
	// contains only the supported extension.
	_, hdr, _ = extTestRPC(t, srv.Endpoint(), http.Header{
		A2AExtensionsHeader: []string{testExtUnknownURI + ", " + testExtURI},
	})
	if got := hdr.Get(A2AExtensionsHeader); got != testExtURI {
		t.Fatalf("echo = %q, want only %q", got, testExtURI)
	}
}

func TestServerRequiredExtensionGate(t *testing.T) {
	srv := startExtTestServer(t, []AgentExtension{
		{URI: testExtReqURI, Required: true},
		{URI: testExtURI},
	})

	// Missing required activation → -32004 with the offending URIs in data.
	_, hdr, rpc := extTestRPC(t, srv.Endpoint(), nil)
	if rpc.Error == nil || rpc.Error.Code != ErrUnsupportedOp.Code {
		t.Fatalf("want -32004 for missing required extension, got %+v", rpc.Error)
	}
	if !strings.Contains(rpc.Error.Data, testExtReqURI) {
		t.Fatalf("error data should name the missing extension, got %q", rpc.Error.Data)
	}
	if v := hdr.Get(A2AExtensionsHeader); v != "" {
		t.Fatalf("no activation to echo, got %q", v)
	}

	// Activating exactly the required extension passes the gate.
	_, hdr, rpc = extTestRPC(t, srv.Endpoint(), http.Header{
		A2AExtensionsHeader: []string{testExtReqURI},
	})
	if rpc.Error != nil && rpc.Error.Code == ErrUnsupportedOp.Code {
		t.Fatalf("activated required extension was rejected: %+v", rpc.Error)
	}
	if got := hdr.Get(A2AExtensionsHeader); got != testExtReqURI {
		t.Fatalf("echo = %q, want %q", got, testExtReqURI)
	}
}

func TestAgentCardExtensions(t *testing.T) {
	exts := []AgentExtension{{URI: testExtURI, Required: true, Params: map[string]interface{}{"k": "v"}}}
	srv := startExtTestServer(t, exts)
	client := &http.Client{Timeout: 10 * time.Second}

	for _, path := range []string{"/.well-known/agent.json", "/.well-known/agent-card.json", "/.well-known/a2a.json"} {
		resp, err := client.Get(srv.Endpoint() + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		var card AgentCard
		err = json.NewDecoder(resp.Body).Decode(&card)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode card from %s: %v", path, err)
		}
		if len(card.Capabilities.Extensions) != 1 || card.Capabilities.Extensions[0].URI != testExtURI {
			t.Fatalf("%s: capabilities.extensions = %+v", path, card.Capabilities.Extensions)
		}
		if len(card.Extensions) != 1 || card.Extensions[0].URI != testExtURI {
			t.Fatalf("%s: legacy top-level extensions = %+v", path, card.Extensions)
		}
	}
}

func TestClientExtensionActivation(t *testing.T) {
	srv := startExtTestServer(t, []AgentExtension{
		{URI: testExtReqURI, Required: true},
		{URI: testExtURI},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("unactivated required extension fails fast", func(t *testing.T) {
		c := NewClient(srv.Endpoint(), "")
		if _, err := c.Discover(ctx); err != nil {
			t.Fatalf("discover: %v", err)
		}
		missing := c.MissingRequiredExtensions()
		if len(missing) != 1 || missing[0] != testExtReqURI {
			t.Fatalf("missing = %v, want [%s]", missing, testExtReqURI)
		}
		if _, err := c.ListTasks(ctx, "", 10); err == nil || !strings.Contains(err.Error(), "not activated") {
			t.Fatalf("expected fail-fast error, got %v", err)
		}
	})

	t.Run("activation sends header and negotiates", func(t *testing.T) {
		c := NewClient(srv.Endpoint(), "", WithActivateExtensions([]string{testExtReqURI, testExtUnknownURI}))
		if _, err := c.Discover(ctx); err != nil {
			t.Fatalf("discover: %v", err)
		}
		if missing := c.MissingRequiredExtensions(); len(missing) != 0 {
			t.Fatalf("unexpected missing: %v", missing)
		}
		// tasks/list must succeed (server echoes the negotiated set in the
		// response header; the request would have been rejected with -32004
		// had the activation header been missing).
		if _, err := c.ListTasks(ctx, "", 10); err != nil {
			t.Fatalf("list tasks with activation: %v", err)
		}
	})
}

func TestClientMissingCardTolerated(t *testing.T) {
	// No card discovered yet: pre-flight must not spuriously fail, and an
	// empty activation list must not send the header.
	c := NewClient("http://127.0.0.1:1", "")
	if missing := c.MissingRequiredExtensions(); len(missing) != 0 {
		t.Fatalf("missing without card = %v", missing)
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.applyExtensions(req); err != nil {
		t.Fatalf("applyExtensions: %v", err)
	}
	if req.Header.Get(A2AExtensionsHeader) != "" {
		t.Fatalf("header set without activation: %q", req.Header.Get(A2AExtensionsHeader))
	}
}
