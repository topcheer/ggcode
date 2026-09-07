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
			name:   "smaller window than reference is excluded",
			models: []string{"gpt-4o"}, // 128k
			refWin: 1000000,
			want:   "",
		},
		{
			name:   "smallest qualifying window wins",
			models: []string{"gpt-4.1", "gpt-4o"}, // 1M vs 128k, both vision
			refWin: 128000,
			want:   "gpt-4o",
		},
		{
			name:   "exact 1M reference matches 1M vision candidate",
			models: []string{"glm-5.3", "glm-5.3-flash"}, // non-vision + 1M vision
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
	// Non-vision active model with a vision sibling whose window is smaller:
	// models.dev gives glm-5.3 a 1048576 window but glm-5.3-flash 1000000 -
	// the "equal 1M" assumption this test originally made no longer holds,
	// and the comparable-window rule must conservatively reject the switch
	// (1000000 < 1048576, the conversation could overflow the sibling).
	if got := VisionTurnModel(newCfg("glm-5.3")); got != "" {
		t.Errorf("VisionTurnModel(glm-5.3) = %q, want empty (flash window 1000000 < active 1048576)", got)
	}
	// Same endpoint, but with a sibling whose window is genuinely large
	// enough: the switch must select it.
	cfgBig := newCfg("glm-5.3")
	epBig := cfgBig.Vendors["zai"].Endpoints["coding"]
	epBig.Models = []string{"glm-5.3", "z-ai-glm-5-3-flash"}
	cfgBig.Vendors["zai"].Endpoints["coding"] = epBig
	if got := VisionTurnModel(cfgBig); got != "z-ai-glm-5-3-flash" {
		t.Errorf("VisionTurnModel(glm-5.3, big sibling) = %q, want z-ai-glm-5-3-flash", got)
	}
	// Vision active model -> no switch needed.
	if got := VisionTurnModel(newCfg("glm-5.3-flash")); got != "" {
		t.Errorf("VisionTurnModel(glm-5.3-flash) = %q, want empty", got)
	}
	// No vision candidates on the endpoint.
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
