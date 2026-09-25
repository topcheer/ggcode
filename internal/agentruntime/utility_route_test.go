package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// utilityTestConfig builds a minimal resolvable config on the openai vendor
// with a frontier primary model. Callers tweak UtilityModel/APIKey per case.
func utilityTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-frontier"
	cfg.Vendors["openai"].Endpoints["api"] = config.EndpointConfig{
		DisplayName:  "OpenAI API",
		Protocol:     "openai",
		BaseURL:      "https://api.openai.com/v1",
		APIKey:       "sk-utility-test",
		DefaultModel: "gpt-frontier",
	}
	return cfg
}

func TestUtilityModelNamePrecedence(t *testing.T) {
	if got := UtilityModelName(nil); got != "" {
		t.Fatalf("UtilityModelName(nil) = %q, want empty", got)
	}

	cfg := utilityTestConfig(t)
	if got := UtilityModelName(cfg); got != "" {
		t.Fatalf("UtilityModelName(unset) = %q, want empty", got)
	}

	cfg.UtilityModel = "  gpt-mini  "
	if got := UtilityModelName(cfg); got != "gpt-mini" {
		t.Fatalf("UtilityModelName(explicit) = %q, want %q (must trim whitespace)", got, "gpt-mini")
	}
}

func TestResolveUtilityProviderCases(t *testing.T) {
	t.Run("unset routes nothing", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		prov, err := ResolveUtilityProvider(cfg)
		if err != nil || prov != nil {
			t.Fatalf("expected (nil, nil) for unset utility_model, got (%v, %v)", prov, err)
		}
	})

	t.Run("same as primary routes nothing", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		cfg.UtilityModel = cfg.Model
		prov, err := ResolveUtilityProvider(cfg)
		if err != nil || prov != nil {
			t.Fatalf("expected (nil, nil) when utility model equals primary, got (%v, %v)", prov, err)
		}
	})

	t.Run("routed to utility model", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		cfg.UtilityModel = "gpt-mini"
		prov, err := ResolveUtilityProvider(cfg)
		if err != nil {
			t.Fatalf("ResolveUtilityProvider() error = %v", err)
		}
		if prov == nil {
			t.Fatal("expected non-nil utility provider")
		}
	})

	t.Run("unresolvable vendor is an error", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		cfg.Vendor = "no-such-vendor"
		cfg.UtilityModel = "gpt-mini"
		prov, err := ResolveUtilityProvider(cfg)
		if err == nil {
			t.Fatal("expected resolution error for unknown vendor")
		}
		if prov != nil {
			t.Fatalf("expected nil provider alongside error, got %v", prov)
		}
	})
}

func TestApplyUtilityProvider(t *testing.T) {
	prov := &fakeApplyProvider{}

	t.Run("nil config is a no-op", func(t *testing.T) {
		a := agent.NewAgent(prov, tool.NewRegistry(), "", 10)
		ApplyUtilityProvider(a, nil)
		if a.UtilityProvider() != nil {
			t.Fatal("nil config must leave utility routing unset")
		}
	})

	t.Run("installs routed provider", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		cfg.UtilityModel = "gpt-mini"
		a := agent.NewAgent(prov, tool.NewRegistry(), "", 10)
		ApplyUtilityProvider(a, cfg)
		if a.UtilityProvider() == nil {
			t.Fatal("expected utility provider to be installed")
		}
	})

	t.Run("resolution failure resets to primary", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		cfg.UtilityModel = "gpt-mini"
		a := agent.NewAgent(prov, tool.NewRegistry(), "", 10)
		ApplyUtilityProvider(a, cfg)
		if a.UtilityProvider() == nil {
			t.Fatal("precondition: utility provider installed")
		}

		// Configuration degrades (vendor gone): routing must reset to the
		// primary provider instead of keeping a stale utility provider.
		cfg.Vendor = "no-such-vendor"
		ApplyUtilityProvider(a, cfg)
		if a.UtilityProvider() != nil {
			t.Fatal("failed resolution must reset utility routing to nil")
		}
	})

	t.Run("empty utility_model resets routing", func(t *testing.T) {
		cfg := utilityTestConfig(t)
		cfg.UtilityModel = "gpt-mini"
		a := agent.NewAgent(prov, tool.NewRegistry(), "", 10)
		ApplyUtilityProvider(a, cfg)
		if a.UtilityProvider() == nil {
			t.Fatal("precondition: utility provider installed")
		}

		cfg.UtilityModel = ""
		ApplyUtilityProvider(a, cfg)
		if a.UtilityProvider() != nil {
			t.Fatal("removing utility_model must reset routing to the primary provider")
		}
	})
}

func TestApplyProviderToAgentReappliesUtilityRouting(t *testing.T) {
	prov := &fakeApplyProvider{}
	cfg := utilityTestConfig(t)
	cfg.UtilityModel = "gpt-mini"

	a := agent.NewAgent(prov, tool.NewRegistry(), "", 10)
	r := config.ResolvedEndpoint{Protocol: "openai", Model: "gpt-frontier"}
	ApplyProviderToAgent(a, prov, &r, cfg)
	if a.UtilityProvider() == nil {
		t.Fatal("ApplyProviderToAgent must install utility routing when configured")
	}

	// Model switch to a config without utility routing must clear it.
	cfg.UtilityModel = ""
	r2 := r
	r2.Model = "other-model"
	ApplyProviderToAgent(a, prov, &r2, cfg)
	if a.UtilityProvider() != nil {
		t.Fatal("ApplyProviderToAgent with no utility model must reset routing")
	}
}
