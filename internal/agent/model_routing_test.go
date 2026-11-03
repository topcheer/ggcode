package agent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newRoutingTestAgent(t *testing.T) *Agent {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	prov, err := provider.NewProvider(&config.ResolvedEndpoint{
		VendorID: "zai",
		Protocol: "openai",
		BaseURL:  "https://example.invalid/v1",
		APIKey:   "test-key",
		Model:    "glm-4.6",
	})
	if err != nil {
		t.Fatalf("NewProvider(main) error = %v", err)
	}
	reg := tool.NewRegistry()
	return NewAgent(prov, reg, "test", 1)
}

func auxRoutingEndpoint(model string) *config.ResolvedEndpoint {
	return &config.ResolvedEndpoint{
		VendorID: "zai",
		Protocol: "openai",
		BaseURL:  "https://example.invalid/v1",
		APIKey:   "test-key",
		Model:    model,
	}
}

func TestAuxRoutingDisabledByDefault(t *testing.T) {
	a := newRoutingTestAgent(t)
	if a.auxResolved != nil {
		t.Fatal("auxResolved should be nil by default")
	}
	if got := a.auxProviderFor(); got != a.provider {
		t.Fatal("disabled routing must return the main provider pointer")
	}
}

func TestSetAuxModelNoops(t *testing.T) {
	a := newRoutingTestAgent(t)
	a.SetAuxModel(auxRoutingEndpoint("glm-4.6"), "")
	if a.auxResolved != nil {
		t.Fatal("empty aux model must not enable routing")
	}
	a.SetAuxModel(auxRoutingEndpoint("glm-4.6"), "glm-4.6")
	if a.auxResolved != nil {
		t.Fatal("aux model identical to main model must not enable routing")
	}
}

func TestSetAuxModelEnablesRouting(t *testing.T) {
	a := newRoutingTestAgent(t)
	a.SetAuxModel(auxRoutingEndpoint("glm-4.6"), "glm-4.5-air")
	if a.auxResolved == nil {
		t.Fatal("distinct aux model must enable routing")
	}
	if a.auxResolved.Model != "glm-4.5-air" {
		t.Fatalf("clone Model = %q, want glm-4.5-air", a.auxResolved.Model)
	}
	if a.auxResolved.BaseURL != "https://example.invalid/v1" {
		t.Fatalf("clone must inherit BaseURL, got %q", a.auxResolved.BaseURL)
	}
	got := a.auxProviderFor()
	if got == nil {
		t.Fatal("auxProviderFor returned nil")
	}
	if got == a.provider {
		t.Fatal("enabled routing must build a separate provider instance")
	}

	if got2 := a.auxProviderFor(); got2 != got {
		t.Fatal("aux provider must be cached across calls")
	}
}

func TestAuxChatDelegates(t *testing.T) {
	a := newRoutingTestAgent(t)

	msgs := []provider.Message{{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("ping")}}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := a.auxChat(ctx, msgs, nil)
	if err == nil {
		t.Log("auxChat unexpectedly succeeded against unreachable endpoint")
	}
}

func TestAuxProviderBuildFailureFallsBack(t *testing.T) {
	a := newRoutingTestAgent(t)
	a.SetAuxModel(auxRoutingEndpoint("glm-4.6"), "glm-4.5-air")

	a.auxResolved.Protocol = "no-such-protocol"
	got := a.auxProviderFor()
	if got != a.provider {
		t.Fatal("failed aux build must fall back to the main provider")
	}
	if !a.auxFailed {
		t.Fatal("auxFailed flag must be set after a build failure")
	}

	if got2 := a.auxProviderFor(); got2 != a.provider {
		t.Fatal("fallback must be permanent")
	}
}
