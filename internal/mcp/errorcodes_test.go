package mcp

// Tests for the MCP 2026-07-28 error-code allocation policy (sa-70).
//
// Spec: https://modelcontextprotocol.io/specification/2026-07-28/changelog
//   - -32020..-32099 is reserved for the MCP specification
//     (-32000..-32019 stays implementation-defined / grandfathered).
//   - HeaderMismatch -32001 -> -32020, MissingRequiredClientCapability
//     -32003 -> -32021, UnsupportedProtocolVersion -32004 -> -32022.
//   - Resource not found changes from -32002 to -32602 (Invalid Params).
//
// Pinned behavior: reserved-range errors carry spec context in their
// message, the original *Error stays reachable via errors.As for
// programmatic classification, initialize recognizes the spec's
// UnsupportedProtocolVersionError in both numberings, and resources/read
// failures normalize the legacy/new not-found reporting.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIsMCPReservedErrorCode(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{-32602, false}, // JSON-RPC standard
		{-32000, false}, // grandfathered implementation-defined zone
		{-32019, false}, // zone boundary
		{-32001, false}, // legacy draft numbering: implementation-defined zone
		{-32020, true},  // reserved range start
		{-32022, true},  // UnsupportedProtocolVersion
		{-32042, true},  // URL elicitation required
		{-32099, true},  // reserved range end
		{-32100, false}, // below range
	}
	for _, tc := range cases {
		if got := isMCPReservedErrorCode(tc.code); got != tc.want {
			t.Errorf("isMCPReservedErrorCode(%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestErrorClassificationHelpers(t *testing.T) {
	if !isUnsupportedProtocolVersionCode(-32022) || !isUnsupportedProtocolVersionCode(-32004) {
		t.Error("both renumbered -32022 and draft -32004 must be recognized as UnsupportedProtocolVersion")
	}
	if isUnsupportedProtocolVersionCode(-32001) {
		t.Error("-32001 is HeaderMismatch, not UnsupportedProtocolVersion")
	}
	if !isResourceNotFoundCode(-32002) || !isResourceNotFoundCode(-32602) {
		t.Error("both legacy -32002 and new -32602 must be recognized as not-found carriers")
	}
	if isResourceNotFoundCode(-32001) {
		t.Error("-32001 must not classify as not-found")
	}
}

func TestDecorateSpecError(t *testing.T) {
	// Reserved-range code gets spec context and stays errors.As-able.
	orig := &Error{Code: -32020, Message: "missing Mcp-Method header"}
	err := decorateSpecError(fmt.Errorf("mcp[srv]: initialize: %w", orig))
	msg := err.Error()
	if !strings.Contains(msg, "HeaderMismatch") || !strings.Contains(msg, "-32020") {
		t.Fatalf("decorated message should name the spec code, got: %s", msg)
	}
	var je *Error
	if !errors.As(err, &je) || je.Code != -32020 {
		t.Fatalf("original *Error must stay reachable via errors.As, got %+v", je)
	}
	if decorateSpecError(err) != err {
		t.Error("decorateSpecError must be idempotent")
	}
	// Implementation-defined zone passes through untouched.
	plain := fmt.Errorf("boom: %w", &Error{Code: -32002, Message: "not found"})
	if got := decorateSpecError(plain); got != plain {
		t.Errorf("implementation-defined zone must pass through, got: %v", got)
	}
	if decorateSpecError(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestInitializeUnsupportedProtocolVersionDiagnostic(t *testing.T) {
	err := fmt.Errorf("mcp[srv]: initialize: %w",
		decorateSpecError(&Error{Code: -32022, Message: "unsupported protocol version"}))
	msg := unsupportedProtocolVersionError("srv", err).Error()
	for _, want := range []string{"UnsupportedProtocolVersion", latestMCPProtocolVersion, "server/discover"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic missing %q: %s", want, msg)
		}
	}
	legacy := fmt.Errorf("mcp[srv]: initialize: %w", &Error{Code: -32004, Message: "bad version"})
	if je, ok := jsonRPCErrorOf(legacy); !ok || !isUnsupportedProtocolVersionCode(je.Code) {
		t.Error("legacy -32004 must classify as UnsupportedProtocolVersion")
	}
}

func TestAnnotateResourceReadError(t *testing.T) {
	legacy := fmt.Errorf("mcp[srv]: resources/read: %w", &Error{Code: -32002, Message: "Resource not found"})
	got := annotateResourceReadError(legacy, "file:///x")
	if !strings.Contains(got.Error(), "resource not found") || !strings.Contains(got.Error(), "file:///x") {
		t.Fatalf("legacy -32002 should normalize to a not-found message, got: %v", got)
	}
	var je *Error
	if !errors.As(got, &je) || je.Code != -32002 {
		t.Fatal("original *Error must stay unwrappable after annotation")
	}

	newCode := fmt.Errorf("mcp[srv]: resources/read: %w", &Error{Code: -32602, Message: "Invalid params"})
	got2 := annotateResourceReadError(newCode, "file:///y")
	if !strings.Contains(got2.Error(), "2026-07-28") {
		t.Fatalf("new -32602 should mention the 2026-07-28 overload, got: %v", got2)
	}

	other := fmt.Errorf("mcp[srv]: resources/read: %w", &Error{Code: -32601, Message: "method not found"})
	if got3 := annotateResourceReadError(other, "file:///z"); got3 != other {
		t.Errorf("unrelated codes must pass through unchanged, got: %v", got3)
	}
}

// errServer starts an httptest MCP server answering every request with the
// given JSON-RPC error object (notifications get 202).
func errServer(t *testing.T, code int, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(body, &req) != nil || req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]any{
				"code":    code,
				"message": message,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// TestHeaderMismatchDecoratedEndToEnd pins the sendRequest wiring: a server
// answering -32020 (HeaderMismatch) yields an error that carries the spec
// annotation AND stays programmatically classifiable via errors.As.
func TestHeaderMismatchDecoratedEndToEnd(t *testing.T) {
	srv := errServer(t, ErrCodeHeaderMismatch, "missing Mcp-Method header")
	defer srv.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "sa70", Type: "http", URL: srv.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result struct{}
	err := client.sendRequest(ctx, "tools/call", map[string]any{"name": "x"}, &result)
	if err == nil {
		t.Fatal("expected HeaderMismatch error")
	}
	if !strings.Contains(err.Error(), "HeaderMismatch") {
		t.Fatalf("error should carry the spec annotation, got: %v", err)
	}
	var je *Error
	if !errors.As(err, &je) || je.Code != ErrCodeHeaderMismatch {
		t.Fatalf("JSON-RPC *Error must stay reachable, got %+v", je)
	}
}

// TestConnectUnsupportedProtocolVersionDiagnostic pins the initialize
// wiring: a server rejecting the handshake with the spec's renumbered
// -32022 gets the actionable version diagnostic, and the legacy -32004
// numbering is recognized too.
func TestConnectUnsupportedProtocolVersionDiagnostic(t *testing.T) {
	for _, code := range []int{ErrCodeUnsupportedProtocolVersion, legacyErrCodeUnsupportedProtocolVersion} {
		srv := errServer(t, code, "unsupported protocol version")
		client := NewClientFromConfig(config.MCPServerConfig{Name: "sa70b", Type: "http", URL: srv.URL})
		if err := client.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := client.Initialize(ctx)
		cancel()
		_ = client.Close()
		srv.Close()
		if err == nil {
			t.Fatalf("code %d: expected initialize failure", code)
		}
		if !strings.Contains(err.Error(), "server rejected the protocol version") ||
			!strings.Contains(err.Error(), "server/discover") {
			t.Fatalf("code %d: want actionable version diagnostic, got: %v", code, err)
		}
	}
}

// TestReadResourceNotFoundEndToEnd pins the ReadResource wiring: the
// legacy -32002 surfaces as a "resource not found" message and the new
// -32602 (Invalid Params) carries the 2026-07-28 ambiguity note.
func TestReadResourceNotFoundEndToEnd(t *testing.T) {
	srv := errServer(t, legacyErrCodeResourceNotFound, "Resource not found: file:///gone")
	defer srv.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "sa70c", Type: "http", URL: srv.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.setNegotiatedState(latestMCPProtocolVersion, ServerCaps{Resources: &ResourcesCapability{}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.ReadResource(ctx, "file:///gone")
	if err == nil {
		t.Fatal("expected resource-read failure")
	}
	if !strings.Contains(err.Error(), "resource not found") {
		t.Fatalf("legacy -32002 should surface as resource not found, got: %v", err)
	}
}
