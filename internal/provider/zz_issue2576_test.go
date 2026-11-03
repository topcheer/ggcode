package provider

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue2576_CopilotBranchAppliesCallPolicy(t *testing.T) {
	resolved := &config.ResolvedEndpoint{
		VendorID:       "github-copilot",
		EndpointID:     "main",
		Protocol:       "copilot",
		BaseURL:        "https://api.githubcopilot.com",
		APIKey:         "ghu-test",
		Model:          "gpt-5",
		MaxTokens:      1024,
		RequestTimeout: 45 * time.Second,
		MaxRetries:     2,
	}
	p, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	cp, ok := p.(*CopilotProvider)
	if !ok {
		t.Fatalf("NewProvider() returned %T, want *CopilotProvider", p)
	}
	if got := cp.policy.requestTimeout; got != 45*time.Second {
		t.Errorf("#2576: policy.requestTimeout = %v, want 45s", got)
	}
	if got := cp.policy.maxRetries; got != 2 {
		t.Errorf("#2576: policy.maxRetries = %d, want 2", got)
	}

	resolved.RequestTimeout, resolved.MaxRetries = 0, 0
	p2, err := NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider() (unset) error = %v", err)
	}
	cp2, _ := p2.(*CopilotProvider)
	if cp2.policy.requestTimeout != 0 || cp2.policy.maxRetries != 0 {
		t.Errorf("unset policy leaked: %+v", cp2.policy)
	}
	if got := cp2.policy.attempts(); got != providerRetryAttempts {
		t.Errorf("attempts() = %d, want provider default %d", got, providerRetryAttempts)
	}
}
