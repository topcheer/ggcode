package mcp

// sa-68: MCP 2026-07-28 stateless protocol core (SEP-2575) client tests.
//
// Coverage matrix:
//   - selectModernVersion / asUnsupportedProtocolVersion unit paths
//   - Discover: modern envelope attached, payload decoded (spec example)
//   - Initialize + stateless opt-in on a modern server: discover probe
//     replaces the initialize handshake, per-request _meta envelope flows
//     on every subsequent request, negotiated state + server identity are
//     populated for the existing capability gates and ServerInfo()
//   - Initialize + stateless opt-in on a legacy server: any probe failure
//     that is not a recognized modern error falls back to the unchanged
//     legacy handshake, and legacy requests stay envelope-free
//   - -32022 UnsupportedProtocolVersionError: typed surfacing, "supported"
//     list parsing, and the no-futile-retry gate (same version rejected →
//     no second attempt)

import (
	"bufio"
	"bytes"
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/config"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedRequest records one request observed by the fake server.
type scriptedRequest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// scriptError shapes a JSON-RPC error response.
type scriptError struct {
	code int
	msg  string
	data any
}

// scriptedServer is a stdio fake server whose responses are decided by a
// handler per method; every request is recorded for assertions.
type scriptedServer struct {
	t *testing.T

	mu       sync.Mutex
	requests []scriptedRequest
	handler  func(method string, params json.RawMessage) (any, *scriptError)
}

func (s *scriptedServer) record(r scriptedRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
}

func (s *scriptedServer) callsOf(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r.Method == method {
			n++
		}
	}
	return n
}

func (s *scriptedServer) paramsOf(method string, call int) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r.Method != method {
			continue
		}
		n++
		if n == call {
			return r.Params
		}
	}
	s.t.Fatalf("no call #%d of %q recorded", call, method)
	return nil
}

// start wires a Client to the scripted server over os.Pipes.
func (s *scriptedServer) start() (*Client, func()) {
	s.t.Helper()
	reqRead, reqWrite, err := os.Pipe()
	if err != nil {
		s.t.Fatalf("request pipe: %v", err)
	}
	respRead, respWrite, err := os.Pipe()
	if err != nil {
		s.t.Fatalf("response pipe: %v", err)
	}
	client := &Client{
		name:      "scripted",
		transport: "stdio",
		stdin:     reqWrite,
		reader:    bufio.NewReader(respRead),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reqRead)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var req scriptedRequest
			if err := json.Unmarshal(line, &req); err != nil || req.ID == 0 {
				continue // malformed or a notification (no id)
			}
			s.record(req)
			if strings.HasPrefix(req.Method, "notifications/") {
				continue
			}
			result, serr := s.handler(req.Method, req.Params)
			var payload []byte
			if serr != nil {
				payload, _ = json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    serr.code,
						"message": serr.msg,
						"data":    serr.data,
					},
				})
			} else {
				payload, _ = json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  result,
				})
			}
			_, _ = respWrite.Write(append(payload, '\n'))
		}
	}()
	cleanup := func() {
		_ = reqWrite.Close()
		_ = respWrite.Close()
		<-done
	}
	return client, cleanup
}

// modernDiscoverResult mirrors the server/discover example from the
// 2026-07-28 spec (server/discover section).
func modernDiscoverResult() map[string]any {
	return map[string]any{
		"resultType":        "complete",
		"supportedVersions": []string{"2026-07-28", "2025-11-25"},
		"capabilities":      map[string]any{"tools": map[string]any{"listChanged": true}},
		"instructions":      "Use the greeting tool politely",
		"ttlMs":             30000,
		"_meta": map[string]any{
			"io.modelcontextprotocol/serverInfo": map[string]any{"name": "greeting", "version": "2.1.0"},
		},
	}
}

func TestSelectModernVersion(t *testing.T) {
	if got := selectModernVersion([]string{"2025-11-25", "2026-07-28"}); got != ProtocolVersion20260728 {
		t.Fatalf("selectModernVersion mutual = %q, want %q", got, ProtocolVersion20260728)
	}
	if got := selectModernVersion([]string{"2025-11-25", "2099-01-01"}); got != "" {
		t.Fatalf("selectModernVersion no-mutual = %q, want empty", got)
	}
	if got := selectModernVersion(nil); got != "" {
		t.Fatalf("selectModernVersion nil = %q, want empty", got)
	}
}

func TestAsUnsupportedProtocolVersion(t *testing.T) {
	data := `{"requested":"2026-07-28","supported":["2025-11-25","2026-07-28"]}`
	wrapped := fmt.Errorf("mcp[svc]: send tools/list: %w", &Error{
		Code: ErrorCodeUnsupportedProtocolVersion,
		Data: json.RawMessage(data),
	})
	uerr, ok := asUnsupportedProtocolVersion(wrapped)
	if !ok {
		t.Fatal("asUnsupportedProtocolVersion did not recognize -32022 through the error chain")
	}
	if uerr.Requested != "2026-07-28" {
		t.Errorf("Requested = %q", uerr.Requested)
	}
	if len(uerr.Supported) != 2 || uerr.Supported[1] != "2026-07-28" {
		t.Errorf("Supported = %v", uerr.Supported)
	}
	if !strings.Contains(uerr.Error(), "2025-11-25, 2026-07-28") {
		t.Errorf("Error() = %q, want the supported list rendered", uerr.Error())
	}
	if _, ok := asUnsupportedProtocolVersion(&Error{Code: ErrorCodeMethodNotFound}); ok {
		t.Error("method-not-found must not be recognized as a version error (dual-era fallback rule)")
	}
}

func TestInjectMetaPreservesCallerMeta(t *testing.T) {
	out := injectMeta([]byte(`{"cursor":"","_meta":{"progressToken":7}}`), map[string]any{
		MetaKeyProtocolVersion: ProtocolVersion20260728,
	})
	var params struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(out, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if params.Meta["progressToken"] != float64(7) {
		t.Errorf("caller _meta member lost: %v", params.Meta)
	}
	if params.Meta[MetaKeyProtocolVersion] != ProtocolVersion20260728 {
		t.Errorf("envelope member missing: %v", params.Meta)
	}
	// Non-object params pass through untouched.
	if got := injectMeta([]byte(`null`), map[string]any{"k": "v"}); string(got) != "null" {
		t.Errorf("non-object params mutated: %s", got)
	}
}

func TestDiscoverModernServer(t *testing.T) {
	srv := &scriptedServer{t: t, handler: func(method string, _ json.RawMessage) (any, *scriptError) {
		if method != "server/discover" {
			return nil, &scriptError{code: ErrorCodeMethodNotFound, msg: "method not found"}
		}
		return modernDiscoverResult(), nil
	}}
	client, cleanup := srv.start()
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if res.ResultType != "complete" {
		t.Errorf("ResultType = %q", res.ResultType)
	}
	if len(res.SupportedVersions) != 2 || res.SupportedVersions[0] != ProtocolVersion20260728 {
		t.Errorf("SupportedVersions = %v", res.SupportedVersions)
	}
	if res.Capabilities.Tools == nil || !res.Capabilities.Tools.ListChanged {
		t.Errorf("Capabilities.Tools = %+v", res.Capabilities.Tools)
	}
	if res.Instructions != "Use the greeting tool politely" {
		t.Errorf("Instructions = %q", res.Instructions)
	}
	if si := res.ServerInfo(); si.Name != "greeting" || si.Version != "2.1.0" {
		t.Errorf("ServerInfo = %+v", si)
	}
	if res.TTLms != 30000 {
		t.Errorf("TTLms = %d", res.TTLms)
	}
	// Discover itself carries the modern envelope even outside stateless
	// operation (it is a modern method; the spec example shows _meta on it).
	var params struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(srv.paramsOf("server/discover", 1), &params); err != nil {
		t.Fatalf("unmarshal discover params: %v", err)
	}
	if params.Meta[MetaKeyProtocolVersion] != ProtocolVersion20260728 {
		t.Errorf("discover _meta protocolVersion = %v", params.Meta[MetaKeyProtocolVersion])
	}
}

func TestInitializeStatelessModernServer(t *testing.T) {
	srv := &scriptedServer{t: t, handler: func(method string, _ json.RawMessage) (any, *scriptError) {
		switch method {
		case "server/discover":
			return modernDiscoverResult(), nil
		case "tools/list":
			return map[string]any{
				"tools": []map[string]any{{
					"name":        "greet",
					"description": "Greet someone",
					"inputSchema": map[string]any{"type": "object"},
				}},
				"ttlMs": 30000,
			}, nil
		default:
			return nil, &scriptError{code: ErrorCodeMethodNotFound, msg: "method not found: " + method}
		}
	}}
	client, cleanup := srv.start()
	client.EnableStateless()
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize (stateless): %v", err)
	}
	// The handshake was never sent: the discover probe replaced it.
	if n := srv.callsOf("initialize"); n != 0 {
		t.Errorf("initialize sent %d times on a modern server, want 0", n)
	}
	if got := client.NegotiatedVersion(); got != ProtocolVersion20260728 {
		t.Errorf("NegotiatedVersion = %q, want %q", got, ProtocolVersion20260728)
	}
	if got := client.ModernVersion(); got != ProtocolVersion20260728 {
		t.Errorf("ModernVersion = %q, want %q", got, ProtocolVersion20260728)
	}
	if res.Instructions != "Use the greeting tool politely" {
		t.Errorf("synthesized Instructions = %q", res.Instructions)
	}
	if si := client.ServerInfo(); si.Name != "greeting" || si.Version != "2.1.0" {
		t.Errorf("ServerInfo = %+v", si)
	}

	// Subsequent requests carry the per-request _meta envelope.
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "greet" {
		t.Fatalf("tools = %+v", tools)
	}
	var params struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(srv.paramsOf("tools/list", 1), &params); err != nil {
		t.Fatalf("unmarshal tools/list params: %v", err)
	}
	if params.Meta[MetaKeyProtocolVersion] != ProtocolVersion20260728 {
		t.Errorf("tools/list _meta protocolVersion = %v", params.Meta[MetaKeyProtocolVersion])
	}
	clientInfo, _ := params.Meta[MetaKeyClientInfo].(map[string]any)
	if clientInfo == nil || clientInfo["name"] != "ggcode" {
		t.Errorf("tools/list _meta clientInfo = %v", params.Meta[MetaKeyClientInfo])
	}
}

func TestInitializeStatelessLegacyFallback(t *testing.T) {
	srv := &scriptedServer{t: t, handler: func(method string, _ json.RawMessage) (any, *scriptError) {
		switch method {
		case "server/discover":
			// Legacy server: does not know the modern method.
			return nil, &scriptError{code: ErrorCodeMethodNotFound, msg: "method not found"}
		case "initialize":
			return map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "legacy", "version": "1.0"},
			}, nil
		case "tools/list":
			return map[string]any{
				"tools": []map[string]any{{"name": "legacy_tool", "inputSchema": map[string]any{"type": "object"}}},
			}, nil
		default:
			return nil, &scriptError{code: ErrorCodeMethodNotFound, msg: "method not found: " + method}
		}
	}}
	client, cleanup := srv.start()
	client.EnableStateless()
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize (legacy fallback): %v", err)
	}
	if n := srv.callsOf("initialize"); n != 1 {
		t.Fatalf("initialize sent %d times, want 1", n)
	}
	if got := client.NegotiatedVersion(); got != "2025-11-25" {
		t.Errorf("NegotiatedVersion = %q, want 2025-11-25", got)
	}
	if got := client.ModernVersion(); got != "" {
		t.Errorf("ModernVersion = %q, want empty in legacy mode", got)
	}
	if si := client.ServerInfo(); si.Name != "legacy" || si.Version != "1.0" {
		t.Errorf("ServerInfo = %+v", si)
	}

	// Legacy traffic must stay byte-compatible: no _meta envelope.
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(srv.paramsOf("tools/list", 1), &params); err != nil {
		t.Fatalf("unmarshal tools/list params: %v", err)
	}
	if _, ok := params["_meta"]; ok {
		t.Error("legacy tools/list carried a _meta envelope; legacy traffic must be unchanged")
	}
}

func TestUnsupportedVersionSurfacesTypedError(t *testing.T) {
	cases := []struct {
		name        string
		supported   []string
		wantRetries int
	}{
		{
			// Server rejects the version we sent but lists it as supported:
			// retrying with the same version is futile (no mutual *change*).
			name:        "same version rejected",
			supported:   []string{"2026-07-28"},
			wantRetries: 1,
		},
		{
			name:        "no mutual version",
			supported:   []string{"2099-01-01"},
			wantRetries: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolsCalls := 0
			srv := &scriptedServer{t: t, handler: func(method string, _ json.RawMessage) (any, *scriptError) {
				switch method {
				case "server/discover":
					return map[string]any{
						"supportedVersions": []string{ProtocolVersion20260728},
						"capabilities":      map[string]any{"tools": map[string]any{}},
					}, nil
				case "tools/list":
					toolsCalls++
					return nil, &scriptError{
						code: ErrorCodeUnsupportedProtocolVersion,
						msg:  "Requested protocol version does not match supported versions",
						data: map[string]any{
							"requested": ProtocolVersion20260728,
							"supported": tc.supported,
						},
					}
				default:
					return nil, &scriptError{code: ErrorCodeMethodNotFound, msg: "method not found: " + method}
				}
			}}
			client, cleanup := srv.start()
			client.EnableStateless()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := client.Initialize(ctx); err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			_, err := client.ListTools(ctx)
			if err == nil {
				t.Fatal("ListTools succeeded; want typed UnsupportedProtocolVersionError")
			}
			var uerr *UnsupportedProtocolVersionError
			if !errors.As(err, &uerr) {
				t.Fatalf("error chain lacks *UnsupportedProtocolVersionError: %v", err)
			}
			if got := strings.Join(uerr.Supported, ","); got != strings.Join(tc.supported, ",") {
				t.Errorf("Supported = %v, want %v", uerr.Supported, tc.supported)
			}
			if uerr.Requested != ProtocolVersion20260728 {
				t.Errorf("Requested = %q", uerr.Requested)
			}
			if got := srv.callsOf("tools/list"); got != tc.wantRetries {
				t.Errorf("tools/list attempts = %d, want %d", got, tc.wantRetries)
			}
		})
	}
}

// TestNewClientFromConfigStatelessWiring closes the last uncovered link:
// the YAML "stateless: true" field must reach Client.EnableStateless, and
// the default must stay legacy.
func TestNewClientFromConfigStatelessWiring(t *testing.T) {
	modern := NewClientFromConfig(config.MCPServerConfig{Name: "modern", Command: "srv", Stateless: true})
	if !modern.StatelessEnabled() {
		t.Error("config stateless: true did not enable the stateless opt-in")
	}
	legacy := NewClientFromConfig(config.MCPServerConfig{Name: "legacy", Command: "srv"})
	if legacy.StatelessEnabled() {
		t.Error("stateless opt-in enabled by default; legacy behavior must be unchanged")
	}
}
