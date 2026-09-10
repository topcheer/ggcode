package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #1743 case 2: feishu must honor the sibling strong-validation contract -
// all three fields set, else skip the primary so the local fallback builds.
func Test1743FeishuSTTStrongValidation(t *testing.T) {
	full := config.IMSTTConfig{Provider: "openai", BaseURL: "https://x", APIKey: "k", Model: "m"}

	// Full global: primary returned.
	if got := resolveFeishuSTTConfig(full, nil); got == nil {
		t.Fatal("full global config must yield a primary")
	}

	// Global url+key WITHOUT model - the exact case that previously built a
	// doomed primary ("STT is not configured" on every voice message, the
	// openai.go gate requires baseURL+apiKey+model) while the same config
	// worked for the siblings' strict check.
	noModel := full
	noModel.Model = ""
	if got := resolveFeishuSTTConfig(noModel, nil); got != nil {
		t.Fatal("partial global config must skip the primary (sibling contract)")
	}

	// Global missing any single gate field: skip. (Provider is NOT part of
	// the gate - openai.go never checks it.)
	for i, mutate := range []func(*config.IMSTTConfig){
		func(c *config.IMSTTConfig) { c.BaseURL = "" },
		func(c *config.IMSTTConfig) { c.APIKey = "" },
		func(c *config.IMSTTConfig) { c.Model = "" },
	} {
		c := full
		mutate(&c)
		if got := resolveFeishuSTTConfig(c, nil); got != nil {
			t.Fatalf("global missing field %d must skip the primary", i)
		}
	}

	// Empty global: skip.
	if got := resolveFeishuSTTConfig(config.IMSTTConfig{}, nil); got != nil {
		t.Fatal("empty global must skip the primary")
	}

	// Partial OVERRIDE (only provider, empty global): must skip - a
	// half-configured primary is just as doomed. (A provider override that
	// COMPLETES a url+key+model global is a valid primary, not partial.)
	extra := map[string]interface{}{"stt": map[string]interface{}{"provider": "openai"}}
	if got := resolveFeishuSTTConfig(config.IMSTTConfig{}, extra); got != nil {
		t.Fatal("partial override over empty global must skip the primary")
	}

	// Full override: primary returned.
	extraFull := map[string]interface{}{"stt": map[string]interface{}{
		"provider": "openai", "baseUrl": "https://y", "apiKey": "k2", "model": "m2"}}
	if got := resolveFeishuSTTConfig(config.IMSTTConfig{}, extraFull); got == nil || got.Model != "m2" {
		t.Fatal("full override must yield a primary with overridden model")
	}
}
