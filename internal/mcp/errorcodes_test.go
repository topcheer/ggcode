package mcp

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
		{-32602, false},
		{-32000, false},
		{-32019, false},
		{-32001, false},
		{-32020, true},
		{-32022, true},
		{-32042, true},
		{-32099, true},
		{-32100, false},
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
