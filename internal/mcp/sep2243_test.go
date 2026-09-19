package mcp

// Tests for SEP-2243 (HTTP header standardization) client behavior.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func b64hdr(s string) string {
	return base64SentinelPrefix + base64.StdEncoding.EncodeToString([]byte(s)) + base64SentinelSuffix
}

func TestEncodeMCPHeaderValue(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
		ok   bool
	}{
		{"us-east-1", "us-east-1", true},
		{"", "", true},
		{true, "true", true},
		{false, "false", true},
		{float64(42), "42", true},
		{float64(0), "0", true},
		{float64(-3), "-3", true},
		// Boundary values: MinInt64 and the largest integral float64 below
		// 2^63 are representable and must encode exactly.
		{float64(-(1 << 63)), "-9223372036854775808", true},
		{float64(1<<63) - 1024, "9223372036854774784", true},
		// Integral float64 values beyond int64 range must be dropped (not
		// silently converted to an implementation-dependent garbage value;
		// amd64 gives MinInt64, arm64 saturates to MaxInt64).
		{float64(1 << 63), "", false},
		{1e300, "", false},
		{-1e300, "", false},
		{1e23, "", false},
		{json.Number("7"), "7", true},
		{7, "7", true},
		{int64(9), "9", true},
		// Fractional, NaN/Inf and non-integer numbers are not representable.
		{float64(7.5), "", false},
		{json.Number("7.5"), "", false},
		{json.Number("abc"), "", false},
		// Non-primitive values are dropped.
		{map[string]interface{}{"a": 1}, "", false},
		{[]interface{}{1}, "", false},
		{nil, "", false},
		// Encoding rules: whitespace padding, non-ASCII, control chars, and
		// values that would be ambiguous with the Base64 sentinel.
		{" padded", b64hdr(" padded"), true},
		{"padded ", b64hdr("padded "), true},
		{"tab\tpad", b64hdr("tab\tpad"), true},
		{"café", b64hdr("café"), true},
		{"line\nbreak", b64hdr("line\nbreak"), true},
		{"=?base64?aGk=?=", b64hdr("=?base64?aGk=?="), true},
		{"plain text ok", "plain text ok", true},
	}
	for _, tc := range cases {
		got, ok := encodeMCPHeaderValue(tc.in)
		if ok != tc.ok {
			t.Errorf("encodeMCPHeaderValue(%#v) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("encodeMCPHeaderValue(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHeaderAnnotationViolations(t *testing.T) {
	valid := json.RawMessage(`{
		"type": "object",
		"properties": {
			"options": {
				"type": "object",
				"properties": {
					"region": {"type": "string", "x-mcp-header": "X-Region"},
					"count": {"type": "integer", "x-mcp-header": "X-Count"},
					"flag": {"type": "boolean", "x-mcp-header": "X-Flag"}
				}
			}
		}
	}`)
	if v := headerAnnotationViolations(valid); len(v) != 0 {
		t.Errorf("valid schema: unexpected violations %v", v)
	}

	cases := []struct {
		name   string
		schema json.RawMessage
	}{
		{"empty value", json.RawMessage(`{"properties":{"a":{"type":"string","x-mcp-header":""}}}`)},
		{"non-string value", json.RawMessage(`{"properties":{"a":{"type":"string","x-mcp-header":42}}}`)},
		{"invalid token", json.RawMessage(`{"properties":{"a":{"type":"string","x-mcp-header":"Bad Header"}}}`)},
		{"duplicate case-insensitive", json.RawMessage(`{"properties":{"a":{"type":"string","x-mcp-header":"X-Custom"},"b":{"type":"string","x-mcp-header":"x-custom"}}}`)},
		{"number type not permitted", json.RawMessage(`{"properties":{"a":{"type":"number","x-mcp-header":"X-N"}}}`)},
		{"object type not permitted", json.RawMessage(`{"properties":{"a":{"type":"object","x-mcp-header":"X-O"}}}`)},
		{"array type not permitted", json.RawMessage(`{"properties":{"a":{"type":"array","x-mcp-header":"X-A"}}}`)},
		{"union with number", json.RawMessage(`{"properties":{"a":{"type":["string","number"],"x-mcp-header":"X-U"}}}`)},
	}
	for _, tc := range cases {
		if v := headerAnnotationViolations(tc.schema); len(v) == 0 {
			t.Errorf("%s: expected violations, got none", tc.name)
		}
	}
}

func TestBuildMCPParamHeaders(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"q": {"type": "string", "x-mcp-header": "Query"},
			"options": {
				"type": "object",
				"properties": {
					"region": {"type": "string", "x-mcp-header": "Region"},
					"count": {"type": "integer", "x-mcp-header": "Count"},
					"meta": {"type": "object", "properties": {"id": {"type": "string", "x-mcp-header": "Meta-Id"}}}
				}
			}
		}
	}`)
	args := map[string]interface{}{
		"q": "search term",
		"options": map[string]interface{}{
			"region": "eu-west-1",
			"count":  float64(3),
			"meta":   map[string]interface{}{"id": "abc"},
		},
	}
	got := buildMCPParamHeaders(schema, args)
	want := [][2]string{
		{"Mcp-Param-Query", "search term"},
		{"Mcp-Param-Region", "eu-west-1"},
		{"Mcp-Param-Count", "3"},
		{"Mcp-Param-Meta-Id", "abc"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d headers (%v), want %d", len(got), got, len(want))
	}
	// Annotation order is nondeterministic (Go map iteration), so compare
	// as multisets.
	wantSet := map[[2]string]int{}
	gotSet := map[[2]string]int{}
	for _, kv := range want {
		wantSet[kv]++
	}
	for _, kv := range got {
		gotSet[kv]++
	}
	for kv, n := range wantSet {
		if gotSet[kv] != n {
			t.Errorf("missing or duplicated header %v (got %d, want %d)", kv, gotSet[kv], n)
		}
	}

	// Null and absent parameters omit the header; non-encodable values are
	// dropped (the server's -32001 path handles the mismatch).
	sparse := buildMCPParamHeaders(schema, map[string]interface{}{
		"q":       nil,
		"options": map[string]interface{}{"region": map[string]interface{}{"nested": true}},
	})
	if len(sparse) != 0 {
		t.Errorf("sparse args: got %v, want none", sparse)
	}
	if buildMCPParamHeaders(schema, nil) != nil {
		t.Error("nil args: expected no headers")
	}
}

// TestSEP2243HTTPHeaders is the end-to-end test: tools/list filters
// annotation-violating tools, tools/call mirrors routing fields and
// annotated parameters into headers, resources/read uses Mcp-Name for the
// URI, notifications carry Mcp-Method, and a -32001 HeaderMismatch
// response triggers a schema refresh + single retry.
func TestSEP2243HTTPHeaders(t *testing.T) {
	var mu sync.Mutex
	callNum := 0
	toolCallNum := 0
	captured := make(map[string]http.Header) // "<callNum>:<method>" -> headers

	toolList := func() []interface{} {
		return []interface{}{
			map[string]interface{}{
				"name":        "annotated",
				"description": "Annotated tool",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"options": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"region": map[string]interface{}{"type": "string", "x-mcp-header": "Region"},
							},
						},
					},
				},
			},
			// Violates SEP-2243 (empty header value): MUST be excluded.
			map[string]interface{}{
				"name":        "badhdr",
				"description": "Violating tool",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"x": map[string]interface{}{"type": "string", "x-mcp-header": ""}},
				},
			},
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var raw map[string]interface{}
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		method, _ := raw["method"].(string)
		mu.Lock()
		callNum++
		captured[strconv.Itoa(callNum)+":"+method] = r.Header.Clone()
		mu.Unlock()

		respond := func(result interface{}) {
			raw["jsonrpc"] = "2.0"
			raw["result"] = result
			delete(raw, "method")
			delete(raw, "params")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(raw)
		}
		switch method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess")
			respond(map[string]interface{}{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "mock", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			respond(map[string]interface{}{"tools": toolList()})
		case "tools/call":
			mu.Lock()
			toolCallNum++
			tcn := toolCallNum
			mu.Unlock()
			if tcn == 1 {
				// First tools/call: simulate -32001 HeaderMismatch.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":3,"error":{"code":-32001,"message":"HeaderMismatch"}}`))
				return
			}
			respond(map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}})
		case "resources/read":
			respond(map[string]interface{}{"contents": []interface{}{}})
		case "notifications/callToolResult":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected method %s", method)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "remote", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	// tools/list excludes the violating tool and caches the valid schema.
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "annotated" {
		t.Fatalf("expected only 'annotated' tool, got %+v", tools)
	}

	// First tools/call gets -32001; the client must refresh (tools/list
	// again) and retry exactly once, succeeding on the second attempt.
	result, err := client.CallTool(context.Background(), "annotated", map[string]interface{}{
		"options": map[string]interface{}{"region": "eu-west-1"},
	})
	if err != nil {
		t.Fatalf("tools/call after -32001 retry: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}

	// The -32001 path consumed exactly one refresh + retry:
	// init, initialized, list, call-400, list(refresh), call-ok.
	if callNum != 6 {
		t.Errorf("expected 6 calls after the successful retry, got %d", callNum)
	}

	if _, err := client.ReadResource(context.Background(), "file:///tmp/example"); err != nil {
		t.Fatal(err)
	}
	_ = client.sendNotification(context.Background(), Notification{JSONRPC: "2.0", Method: "notifications/callToolResult"})

	mu.Lock()
	defer mu.Unlock()
	assertHeader := func(key string, header string, want string) {
		h, ok := captured[key]
		if !ok {
			t.Fatalf("no captured request for %s", key)
		}
		if got := h.Get(header); got != want {
			t.Errorf("%s: %s = %q, want %q", key, header, got, want)
		}
	}
	// The retried tools/call (call #6) carries SEP-2243 headers.
	assertHeader("6:tools/call", "Mcp-Method", "tools/call")
	assertHeader("6:tools/call", "Mcp-Name", "annotated")
	assertHeader("6:tools/call", "Mcp-Param-Region", "eu-west-1")
	// resources/read (call #7) mirrors the URI into Mcp-Name.
	assertHeader("7:resources/read", "Mcp-Method", "resources/read")
	assertHeader("7:resources/read", "Mcp-Name", "file:///tmp/example")
	// The notification (call #8) carries Mcp-Method only.
	assertHeader("8:notifications/callToolResult", "Mcp-Method", "notifications/callToolResult")
	if h := captured["8:notifications/callToolResult"]; h.Get("Mcp-Name") != "" {
		t.Errorf("notification unexpectedly carried Mcp-Name: %q", h.Get("Mcp-Name"))
	}
	// Exactly one retry happened: init, initialized, list, call-400,
	// list(refresh), call-ok, read, notif.
	if callNum != 8 {
		t.Errorf("expected 8 total calls, got %d", callNum)
	}
}
