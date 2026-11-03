package provider

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestCallPolicyAttempts(t *testing.T) {
	if got := (callPolicy{}).attempts(); got != providerRetryAttempts {
		t.Errorf("unset policy: attempts() = %d, want %d", got, providerRetryAttempts)
	}
	if got := (callPolicy{maxRetries: 3}).attempts(); got != 3 {
		t.Errorf("override: attempts() = %d, want 3", got)
	}
}

func TestCallPolicyWithTimeout(t *testing.T) {
	baseCtx := context.Background()
	ctx, cancel := (callPolicy{}).withTimeout(baseCtx)
	defer cancel()
	if ctx == nil {
		t.Fatal("unset policy: ctx must never be nil")
	}
	if _, ok := ctx.Deadline(); ok {
		t.Error("unset policy: ctx must not carry a deadline")
	}

	ctx2, cancel2 := (callPolicy{requestTimeout: 25 * time.Millisecond}).withTimeout(baseCtx)
	defer cancel2()
	deadline, ok := ctx2.Deadline()
	if !ok {
		t.Fatal("configured policy: ctx must carry a deadline")
	}
	select {
	case <-ctx2.Done():
		if ctx2.Err() != context.DeadlineExceeded {
			t.Errorf("err = %v, want DeadlineExceeded", ctx2.Err())
		}
	case <-time.After(time.Until(deadline) + 100*time.Millisecond):
		t.Fatal("deadline never fired")
	}
}

func TestNewProviderAppliesCallPolicy(t *testing.T) {
	resolved := &config.ResolvedEndpoint{
		VendorID:       "acme",
		EndpointID:     "relay",
		Protocol:       "openai",
		BaseURL:        "https://relay.example.com/v1",
		APIKey:         "sk-test",
		Model:          "gpt-5-nano",
		MaxTokens:      1024,
		RequestTimeout: 42 * time.Second,
		MaxRetries:     3,
	}
	p, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("NewProvider() returned %T, want *OpenAIProvider", p)
	}
	if op.policy.requestTimeout != 42*time.Second {
		t.Errorf("policy.requestTimeout = %v, want 42s", op.policy.requestTimeout)
	}
	if op.policy.maxRetries != 3 {
		t.Errorf("policy.maxRetries = %d, want 3", op.policy.maxRetries)
	}

	resolved.RequestTimeout, resolved.MaxRetries = 0, 0
	p2, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider() (unset) error = %v", err)
	}
	op2, ok2 := p2.(*OpenAIProvider)
	if !ok2 {
		t.Fatalf("NewProvider() (unset) returned %T, want *OpenAIProvider", p2)
	}
	if op2.policy.attempts() != providerRetryAttempts {
		t.Errorf("unset policy attempts = %d, want %d", op2.policy.attempts(), providerRetryAttempts)
	}
}

func TestCloneWithModelKeepsPolicy(t *testing.T) {
	src := &OpenAIProvider{model: "a", policy: callPolicy{requestTimeout: time.Minute, maxRetries: 2}}
	clone, ok := src.CloneWithModel("b").(*OpenAIProvider)
	if !ok {
		t.Fatalf("CloneWithModel returned %T", src.CloneWithModel("b"))
	}
	if clone.policy != src.policy {
		t.Errorf("openai clone policy = %+v, want %+v", clone.policy, src.policy)
	}

	asrc := &AnthropicProvider{model: "a", policy: callPolicy{requestTimeout: 30 * time.Second, maxRetries: 1}}
	aclone, ok := asrc.CloneWithModel("b").(*AnthropicProvider)
	if !ok {
		t.Fatalf("CloneWithModel returned %T", asrc.CloneWithModel("b"))
	}
	if aclone.policy != asrc.policy {
		t.Errorf("anthropic clone policy = %+v, want %+v", aclone.policy, asrc.policy)
	}

	gsrc := &GeminiProvider{model: "a", policy: callPolicy{requestTimeout: 10 * time.Second}}
	gclone, ok := gsrc.CloneWithModel("b").(*GeminiProvider)
	if !ok {
		t.Fatalf("CloneWithModel returned %T", gsrc.CloneWithModel("b"))
	}
	if gclone.policy != gsrc.policy {
		t.Errorf("gemini clone policy = %+v, want %+v", gclone.policy, gsrc.policy)
	}
}
