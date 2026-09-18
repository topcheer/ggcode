package provider

// sa-81: service_tier support in the OpenAI chat adapter. The tier hint must
// ride the request's `service_tier` field, survive model swaps, and degrade
// gracefully when an OpenAI-compatible gateway rejects it.

import (
	"errors"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestOpenAISetServiceTierValidation(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("k", "gpt-5", 0, "http://localhost")
	if p.ServiceTier() != "" {
		t.Fatalf("initial tier = %q, want empty", p.ServiceTier())
	}
	p.SetServiceTier(" Flex ")
	if p.ServiceTier() != "flex" {
		t.Errorf("tier = %q, want flex (trimmed+lowercased)", p.ServiceTier())
	}
	p.SetServiceTier("turbo")
	if p.ServiceTier() != "flex" {
		t.Errorf("invalid value changed tier to %q, want flex preserved", p.ServiceTier())
	}
	p.SetServiceTier("")
	if p.ServiceTier() != "" {
		t.Errorf("empty value did not clear tier, got %q", p.ServiceTier())
	}
}

func TestOpenAIApplyServiceTier(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("k", "gpt-5", 0, "http://localhost")
	req := openai.ChatCompletionRequest{}
	if p.applyServiceTier(&req) {
		t.Fatalf("apply with unset tier must be a no-op")
	}
	p.SetServiceTier("fast")
	req = openai.ChatCompletionRequest{}
	if !p.applyServiceTier(&req) || req.ServiceTier != "fast" {
		t.Fatalf("tier not applied: got %q", req.ServiceTier)
	}
}

func TestOpenAICloneKeepsServiceTier(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("k", "gpt-5", 0, "http://localhost")
	p.SetServiceTier("scale")
	clone, ok := p.CloneWithModel("gpt-5-mini").(*OpenAIProvider)
	if !ok {
		t.Fatalf("CloneWithModel did not return *OpenAIProvider")
	}
	if clone.ServiceTier() != "scale" {
		t.Errorf("clone tier = %q, want scale carried over", clone.ServiceTier())
	}
}

func TestRetryWithoutServiceTier(t *testing.T) {
	param := "service_tier"
	err := &openai.APIError{Message: "Unknown parameter: 'service_tier'.", Param: &param}
	if !retryWithoutServiceTier(err) {
		t.Fatalf("expected true for service_tier param rejection")
	}
	if retryWithoutServiceTier(errors.New("connection reset by peer")) {
		t.Fatalf("expected false for unrelated error")
	}
}
