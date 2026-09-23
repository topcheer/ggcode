package mcp

// sa-144: coverage for migration equivalence helpers and the MRTR deferred
// input-request resolvers (elicitation / sampling / roots). Pure-function
// table tests plus handler-injected resolver paths - no transport mocking of
// internal functions.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSa144SameStringMap(t *testing.T) {
	if !sameStringMap(nil, nil) || !sameStringMap(nil, map[string]string{}) {
		t.Fatal("nil and empty maps must be equivalent")
	}
	a := map[string]string{"A": "1", "B": "2"}
	if !sameStringMap(a, map[string]string{"B": "2", "A": "1"}) {
		t.Fatal("order-independent equality expected")
	}
	if sameStringMap(a, map[string]string{"A": "1", "B": "3"}) {
		t.Fatal("value mismatch must differ")
	}
	if sameStringMap(a, map[string]string{"A": "1"}) {
		t.Fatal("missing key must differ")
	}
	if sameStringMap(a, map[string]string{"A": "1", "B": "2", "C": "3"}) {
		t.Fatal("extra key must differ")
	}
}

func TestSa144NormalizedTransport(t *testing.T) {
	if got := normalizedTransport(""); got != "stdio" {
		t.Fatalf("empty transport defaults to stdio, got %q", got)
	}
	if got := normalizedTransport("  HTTP "); got != "http" {
		t.Fatalf("expected trimmed lowercase http, got %q", got)
	}
}

func TestSa144SameServerConfig(t *testing.T) {
	base := config.MCPServerConfig{
		Name:    "srv",
		Type:    "stdio",
		Command: "cmd",
		URL:     "https://x",
		Args:    []string{"-a", "-b"},
		Env:     map[string]string{"K": "V"},
		Headers: map[string]string{"H": "1"},
	}
	if !sameServerConfig(base, base) {
		t.Fatal("identical configs must be equal")
	}
	// Name is trimmed before comparison.
	trimmed := base
	trimmed.Name = "  srv  "
	if !sameServerConfig(base, trimmed) {
		t.Fatal("name comparison must trim whitespace")
	}
	// Empty transport equals stdio.
	noType := base
	noType.Type = ""
	if !sameServerConfig(base, noType) {
		t.Fatal("empty transport must normalize to stdio")
	}
	cases := []struct {
		name string
		mut  func(c *config.MCPServerConfig)
	}{
		{"name", func(c *config.MCPServerConfig) { c.Name = "other" }},
		{"type", func(c *config.MCPServerConfig) { c.Type = "http" }},
		{"command", func(c *config.MCPServerConfig) { c.Command = "cmd2" }},
		{"url", func(c *config.MCPServerConfig) { c.URL = "https://y" }},
		{"args-len", func(c *config.MCPServerConfig) { c.Args = []string{"-a"} }},
		{"args-elem", func(c *config.MCPServerConfig) { c.Args = []string{"-a", "-c"} }},
		{"env", func(c *config.MCPServerConfig) { c.Env = map[string]string{"K": "W"} }},
		{"headers", func(c *config.MCPServerConfig) { c.Headers = map[string]string{"H": "2"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			tc.mut(&mutated)
			if sameServerConfig(base, mutated) {
				t.Fatalf("configs must differ after mutating %s", tc.name)
			}
		})
	}
}

func TestSa144SameServerSet(t *testing.T) {
	a := []config.MCPServerConfig{{Name: "one", Command: "c"}, {Name: "two", Command: "d"}}
	b := []config.MCPServerConfig{{Name: "one", Command: "c"}, {Name: "two", Command: "d"}}
	if !sameServerSet(a, b) {
		t.Fatal("identical sets must be equal")
	}
	if sameServerSet(a, a[:1]) {
		t.Fatal("length mismatch must differ")
	}
	swapped := []config.MCPServerConfig{{Name: "one", Command: "c"}, {Name: "two", Command: "CHANGED"}}
	if sameServerSet(a, swapped) {
		t.Fatal("element mismatch must differ")
	}
}

func TestSa144SetMRTRRetryAllParams(t *testing.T) {
	responses := map[string]json.RawMessage{"req-1": json.RawMessage(`{"action":"accept"}`)}
	var params mrtrRetryParams
	params = &CallToolParams{}
	params.setMRTRRetry(responses, "s1")
	if got, ok := params.(*CallToolParams); !ok || got.RequestState != "s1" || len(got.InputResponses) != 1 {
		t.Fatalf("CallToolParams retry fields not set: %+v", params)
	}
	params = &GetPromptParams{}
	params.setMRTRRetry(responses, "s2")
	if got, ok := params.(*GetPromptParams); !ok || got.RequestState != "s2" || len(got.InputResponses) != 1 {
		t.Fatalf("GetPromptParams retry fields not set: %+v", params)
	}
	params = &ReadResourceParams{}
	params.setMRTRRetry(responses, "s3")
	if got, ok := params.(*ReadResourceParams); !ok || got.RequestState != "s3" || len(got.InputResponses) != 1 {
		t.Fatalf("ReadResourceParams retry fields not set: %+v", params)
	}
	if len(responses) != 1 {
		t.Fatal("responses map must not be mutated")
	}
}

func TestSa144ResolveMRTElicitation(t *testing.T) {
	c := NewClient("sa144", "echo", nil)
	ctx := context.Background()

	// No handler registered.
	if _, err := c.resolveMRTElicitation(ctx, json.RawMessage(`{"message":"m"}`)); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected not-supported error, got %v", err)
	}

	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return &ElicitationResult{Action: ElicitationActionAccept, Content: map[string]any{"name": "val"}}, nil
	})

	// Malformed params JSON.
	if _, err := c.resolveMRTElicitation(ctx, json.RawMessage(`{bad json`)); err == nil || !strings.Contains(err.Error(), "invalid elicitation params") {
		t.Fatalf("expected invalid-params error, got %v", err)
	}

	// Form mode with valid schema.
	form := json.RawMessage(`{"message":"name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}`)
	out, err := c.resolveMRTElicitation(ctx, form)
	if err != nil {
		t.Fatalf("form resolve: %v", err)
	}
	if !strings.Contains(string(out), "accept") {
		t.Fatalf("expected accept action in %s", out)
	}

	// Form mode with schema missing properties fails validation.
	bad := json.RawMessage(`{"message":"name?","requestedSchema":{"type":"object"}}`)
	if _, err := c.resolveMRTElicitation(ctx, bad); err == nil || !strings.Contains(err.Error(), "invalid elicitation params") {
		t.Fatalf("expected schema validation error, got %v", err)
	}

	// URL mode: content is stripped from the response (out-of-band flow).
	urlMode := json.RawMessage(`{"mode":"url","message":"auth","url":"https://example.com/link","elicitationId":"e1"}`)
	out, err = c.resolveMRTElicitation(ctx, urlMode)
	if err != nil {
		t.Fatalf("url resolve: %v", err)
	}
	if strings.Contains(string(out), "content") {
		t.Fatalf("URL-mode response must not carry content: %s", out)
	}
	if !strings.Contains(string(out), "accept") {
		t.Fatalf("expected accept action in %s", out)
	}

	// Handler failure surfaces as elicitation failed.
	c.SetElicitationHandler(func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
		return nil, context.DeadlineExceeded
	})
	if _, err := c.resolveMRTElicitation(ctx, form); err == nil || !strings.Contains(err.Error(), "elicitation failed") {
		t.Fatalf("expected handler error, got %v", err)
	}
}

func TestSa144ResolveMRTSampling(t *testing.T) {
	c := NewClient("sa144", "echo", nil)
	ctx := context.Background()

	if _, err := c.resolveMRTSampling(ctx, json.RawMessage(`{"messages":[]}`)); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected not-supported error, got %v", err)
	}

	c.SetSamplingHandler(func(ctx context.Context, params SamplingParams) (*SamplingResult, error) {
		return &SamplingResult{Model: "m", StopReason: "end_turn", Role: "assistant", Content: SamplingContent{Type: "text", Text: "ok"}}, nil
	})

	// #2514 structural validation: toolChoice without tools is rejected
	// before reaching the LLM handler.
	invalid := json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}],"toolChoice":{"mode":"auto"}}`)
	if _, err := c.resolveMRTSampling(ctx, invalid); err == nil || !strings.Contains(err.Error(), "invalid sampling params") {
		t.Fatalf("expected validation error, got %v", err)
	}

	valid := json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}],"maxTokens":100}`)
	out, err := c.resolveMRTSampling(ctx, valid)
	if err != nil {
		t.Fatalf("sampling resolve: %v", err)
	}
	if !strings.Contains(string(out), `"text":"ok"`) {
		t.Fatalf("unexpected sampling result: %s", out)
	}

	c.SetSamplingHandler(func(ctx context.Context, params SamplingParams) (*SamplingResult, error) {
		return nil, context.Canceled
	})
	if _, err := c.resolveMRTSampling(ctx, valid); err == nil || !strings.Contains(err.Error(), "sampling failed") {
		t.Fatalf("expected handler error, got %v", err)
	}
}

func TestSa144ResolveMRTRoots(t *testing.T) {
	out, err := resolveMRTRoots()
	if err != nil {
		t.Fatalf("resolveMRTRoots: %v", err)
	}
	if !strings.Contains(string(out), `"roots"`) {
		t.Fatalf("expected roots array, got %s", out)
	}
}
