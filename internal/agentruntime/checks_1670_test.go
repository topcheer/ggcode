package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func Test1670EmptyVendorInheritsCurrent(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "zai",
		Endpoint: "main",
		Vendors: map[string]config.VendorConfig{
			"zai": {Endpoints: map[string]config.EndpointConfig{
				"main": {BaseURL: "https://api.z.ai/v1", APIKey: "k", Protocol: "openai", SelectedModel: "glm-5.3"},
			}},
		},
	}

	if _, _, err := ActivateCurrentSelection(cfg, "", "", "glm-5.3-vision"); err != nil {
		t.Fatalf("vision-form switch must inherit the current selection, got: %v", err)
	}
	if cfg.Model != "glm-5.3-vision" {
		t.Fatalf("model must switch, got %q", cfg.Model)
	}

	if _, _, err := ActivateCurrentSelection(cfg, "", "", "glm-5.3"); err != nil {
		t.Fatalf("restore-form switch must inherit too, got: %v", err)
	}
}
