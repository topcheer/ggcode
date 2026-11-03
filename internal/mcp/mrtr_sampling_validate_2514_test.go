package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveMRTSamplingValidatesStructure(t *testing.T) {
	c := NewClient("val-test", "true", nil)
	c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
		t.Fatal("handler must not be reached for structurally invalid params")
		return nil, nil
	})

	raw := json.RawMessage(`{
		"messages": [{"role": "user", "content": {"type": "text", "text": "hi"}}],
		"maxTokens": 100,
		"toolChoice": {"mode": "auto"}
	}`)
	_, err := c.resolveMRTSampling(context.Background(), raw)
	if err == nil {
		t.Fatal("expected structural validation error (#2514), got nil")
	}
	if !strings.Contains(err.Error(), "invalid sampling params") {
		t.Fatalf("error should surface as invalid sampling params, got %v", err)
	}
}

func TestResolveMRTSamplingAcceptsValidParams(t *testing.T) {
	c := NewClient("val-test2", "true", nil)
	called := false
	c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
		called = true
		return &SamplingResult{Model: "m", Content: SamplingContent{Type: "text", Text: "ok"}}, nil
	})

	raw := json.RawMessage(`{
		"messages": [{"role": "user", "content": {"type": "text", "text": "hi"}}],
		"maxTokens": 100
	}`)
	out, err := c.resolveMRTSampling(context.Background(), raw)
	if err != nil {
		t.Fatalf("valid params must pass validation: %v", err)
	}
	if !called {
		t.Fatal("handler must be reached for valid params")
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("unexpected result: %s", out)
	}
}
