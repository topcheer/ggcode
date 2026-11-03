package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSelectVisionModel(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		refWin int
		want   string
	}{
		{
			name:   "vision model picked over non-vision",
			models: []string{"deepseek-chat", "gpt-4o"},
			refWin: 128000,
			want:   "gpt-4o",
		},
		{
			name:   "no candidate fits - last resort returns largest vision window",
			models: []string{"gpt-4o"},
			refWin: 1000000,
			want:   "gpt-4o",
		},
		{
			name:   "smallest qualifying window wins",
			models: []string{"gpt-4.1", "gpt-4o"},
			refWin: 128000,
			want:   "gpt-4o",
		},
		{
			name:   "exact 1M reference matches 1M vision candidate",
			models: []string{"glm-5.3", "glm-5.3-flash"},
			refWin: 1000000,
			want:   "glm-5.3-flash",
		},
		{
			name:   "no vision candidates at all",
			models: []string{"deepseek-chat", "glm-5.3"},
			refWin: 128000,
			want:   "",
		},
		{
			name:   "empty list",
			models: nil,
			refWin: 128000,
			want:   "",
		},
		{
			name:   "zero reference window accepts any vision model",
			models: []string{"gpt-4o", "gpt-4.1"},
			refWin: 0,
			want:   "gpt-4o",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SelectVisionModel(tt.models, tt.refWin); got != tt.want {
				t.Errorf("SelectVisionModel(%v, %d) = %q, want %q", tt.models, tt.refWin, got, tt.want)
			}
		})
	}
}

func TestVisionTurnModel(t *testing.T) {
	newCfg := func(model string) *config.Config {
		return &config.Config{
			Vendor:   "zai",
			Endpoint: "coding",
			Model:    model,
			Vendors: map[string]config.VendorConfig{
				"zai": {Endpoints: map[string]config.EndpointConfig{
					"coding": {
						Protocol: "openai",
						BaseURL:  "https://api.example.com",
						Models:   []string{"glm-5.3", "glm-5.3-flash"},
					},
				}},
			},
		}
	}

	if got := VisionTurnModel(newCfg("glm-5.3")); got != "glm-5.3-flash" {
		t.Errorf("VisionTurnModel(glm-5.3) = %q, want glm-5.3-flash (last-resort largest vision window)", got)
	}

	cfgBig := newCfg("glm-5.3")
	epBig := cfgBig.Vendors["zai"].Endpoints["coding"]
	epBig.Models = []string{"glm-5.3", "z-ai-glm-5-3-flash"}
	cfgBig.Vendors["zai"].Endpoints["coding"] = epBig
	if got := VisionTurnModel(cfgBig); got != "z-ai-glm-5-3-flash" {
		t.Errorf("VisionTurnModel(glm-5.3, big sibling) = %q, want z-ai-glm-5-3-flash", got)
	}

	if got := VisionTurnModel(newCfg("glm-5.3-flash")); got != "" {
		t.Errorf("VisionTurnModel(glm-5.3-flash) = %q, want empty", got)
	}

	cfg := newCfg("deepseek-chat")
	ep := cfg.Vendors["zai"].Endpoints["coding"]
	ep.Models = []string{"deepseek-chat", "glm-5.3"}
	cfg.Vendors["zai"].Endpoints["coding"] = ep
	if got := VisionTurnModel(cfg); got != "" {
		t.Errorf("VisionTurnModel(no vision candidates) = %q, want empty", got)
	}
	if got := VisionTurnModel(nil); got != "" {
		t.Errorf("VisionTurnModel(nil) = %q, want empty", got)
	}
}

func TestVisionTurnModelWithPopulatedZaiModels(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "zai",
		Endpoint: "cn-coding",
	}
	cfg.Vendors = map[string]config.VendorConfig{
		"zai": {
			Endpoints: map[string]config.EndpointConfig{
				"cn-coding": {
					Protocol:      "openai",
					BaseURL:       "https://api.z.ai/api/coding/paas/v4",
					SelectedModel: "glm-5.3",

					Models: []string{"glm-5.3-flash", "glm-5.3", "glm-5.3-highspeed", "glm-5.2", "glm-5.2-highspeed", "glm-5-turbo", "glm-4.7"},
				},
			},
		},
	}
	ep := cfg.Vendors["zai"].Endpoints["cn-coding"]

	vm := VisionTurnModel(cfg)
	if vm == "" {
		t.Fatalf("VisionTurnModel returned empty; models=%v", ep.Models)
	}
	if vm != "glm-5.3-flash" {
		t.Errorf("VisionTurnModel() = %q, want glm-5.3-flash (smallest vision model >= user window)", vm)
	}
}
